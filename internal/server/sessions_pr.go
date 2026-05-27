package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const sessionsCurrentSchemaVersion = 1

const namespaceSlugMaxLen = 32

var (
	sessionSlugPattern  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}-[a-z0-9-]+-[a-f0-9]{6,}$`)
	namespaceCleanup    = regexp.MustCompile(`[^a-z0-9-]+`)
	sessionRequiredHdrs = []string{"## Summary", "## Timeline", "## What was tried", "## Findings", "## Outcome"}
)

type sessionAuthor struct {
	Name  string `yaml:"name"  json:"name"`
	Email string `yaml:"email" json:"email"`
}

type sessionSources struct {
	IncidentIO string `yaml:"incident_io,omitempty" json:"incident_io,omitempty"`
	Slack      string `yaml:"slack,omitempty"       json:"slack,omitempty"`
	Bundle     string `yaml:"bundle"                json:"bundle"`
}

type sessionFrontmatter struct {
	SchemaVersion   int            `yaml:"schema_version"             json:"schema_version"`
	ID              string         `yaml:"id"                         json:"id"`
	Date            string         `yaml:"date"                       json:"date"`
	Title           string         `yaml:"title"                      json:"title"`
	Author          sessionAuthor  `yaml:"author"                     json:"author"`
	Namespace       string         `yaml:"namespace"                  json:"namespace"`
	ClustersTouched []string       `yaml:"clusters_touched,omitempty" json:"clusters_touched,omitempty"`
	// ContextName is kept for tolerant parsing of legacy session.md files
	// written before E3. Never emitted — omitempty ensures it is skipped
	// when zero.
	ContextName string `yaml:"context_name,omitempty" json:"-"`
	Sources     sessionSources `yaml:"sources" json:"sources"`
}

// computeSessionSlug builds the per-session directory name. namespace is
// lowercased, non-[a-z0-9-] collapsed to '-', truncated to 32 chars.
// id is the existing opaque investigation id; we take the first 6 chars.
func computeSessionSlug(createdAt time.Time, namespace, id string) string {
	nsLower := strings.ToLower(namespace)
	nsClean := namespaceCleanup.ReplaceAllString(nsLower, "-")
	nsClean = strings.Trim(nsClean, "-")
	if len(nsClean) > namespaceSlugMaxLen {
		nsClean = nsClean[:namespaceSlugMaxLen]
	}
	if nsClean == "" {
		nsClean = "ns"
	}
	idShort := id
	if len(idShort) > 6 {
		idShort = idShort[:6]
	}
	return fmt.Sprintf("%s-%s-%s",
		createdAt.UTC().Format("2006-01-02"),
		nsClean,
		idShort,
	)
}

func validateSessionFrontmatter(fm sessionFrontmatter) []string {
	var errs []string
	if fm.SchemaVersion != 0 && fm.SchemaVersion != sessionsCurrentSchemaVersion {
		errs = append(errs, fmt.Sprintf("schema_version %d unsupported; this build understands %d", fm.SchemaVersion, sessionsCurrentSchemaVersion))
	}
	if !sessionSlugPattern.MatchString(fm.ID) {
		errs = append(errs, fmt.Sprintf("id %q does not match the session slug pattern", fm.ID))
	}
	if strings.TrimSpace(fm.Date) == "" {
		errs = append(errs, "date is required (YYYY-MM-DD)")
	}
	if strings.TrimSpace(fm.Title) == "" {
		errs = append(errs, "title is required")
	}
	if strings.TrimSpace(fm.Author.Name) == "" {
		errs = append(errs, "author.name is required")
	}
	if strings.TrimSpace(fm.Author.Email) == "" {
		errs = append(errs, "author.email is required")
	}
	return errs
}

func validateSessionBodyHeaders(body string) []string {
	var errs []string
	for _, h := range sessionRequiredHdrs {
		if !strings.Contains(body, h) {
			errs = append(errs, fmt.Sprintf("missing required header %q", h))
		}
	}
	return errs
}

// monthDirForSlug returns the YYYY-MM directory under sessions/ that a slug
// belongs in.
func monthDirForSlug(slug string) string {
	if len(slug) >= 7 {
		return slug[:7]
	}
	return ""
}

// sessionPushRequest carries optional overrides for the push-PR flow.
type sessionPushRequest struct {
	Branch string
	Title  string
	Body   string
	Base   string
}

// sessionPushResult is the success payload from pushSessionPR.
type sessionPushResult struct {
	URL    string `json:"url"`
	Branch string `json:"branch"`
	Base   string `json:"base"`
	Slug   string `json:"slug"`
}

// sessionDrafter writes the AI-drafted session.md to outPath. Production
// implementation shells out to `triagent-mcp --kind sessions propose-draft`;
// tests pass a stub.
type sessionDrafter func(ctx context.Context, metadataPath, eventsPath, outPath string) error

// pushSessionPR runs the wiki-style push flow for a single archived session:
//  1. caps check (Sessions.Valid + gh auth)
//  2. read metadata, compute slug + month dir
//  3. dirty-tree check
//  4. spawn drafter → writes <proposalsPath>/<proposalID>.md
//  5. read draft, validate + parse frontmatter (overlaying deterministic fields from metadata)
//  6. build the c1inv.json bundle
//  7. fetch base, side branch from origin/<base>
//  8. write session.md + session.triagent.json
//  9. commit, push, gh pr create
//  10. delete proposal, return URL
// pushSessionPR runs the push flow for one archived session. cloneRoot
// is the cwd for git invocations; sessionsPath is the work-dir where
// per-month/per-slug subdirs land (equal to cloneRoot on flat layouts,
// or `cloneRoot/<sessions_path>` when the profile sets one).
func pushSessionPR(
	ctx context.Context,
	caps Capabilities,
	cloneRoot string,
	sessionsPath string,
	upstreamRepo string,
	proposalsPath string,
	sessionDir string,
	req sessionPushRequest,
	drafter sessionDrafter,
) (*sessionPushResult, []string, error) {
	if upstreamRepo == "" {
		upstreamRepo = "sourcehawk/triagent-sessions"
	}
	if !caps.GH.Authenticated {
		return nil, nil, fmt.Errorf("gh CLI not ready: %s", caps.GH.Reason)
	}
	if !caps.Sessions.Valid {
		return nil, nil, fmt.Errorf("sessions vault not ready: %s", caps.Sessions.Reason)
	}

	metaBytes, err := os.ReadFile(filepath.Join(sessionDir, fileMetadata))
	if err != nil {
		return nil, nil, fmt.Errorf("read metadata: %w", err)
	}
	var meta persistedMetadata
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return nil, nil, fmt.Errorf("parse metadata: %w", err)
	}
	createdAt, _ := parseTimestamp(meta.CreatedAt)
	slug := computeSessionSlug(createdAt, meta.Namespace, meta.ID)
	if !sessionSlugPattern.MatchString(slug) {
		return nil, nil, fmt.Errorf("computed slug %q is invalid", slug)
	}
	monthDir := monthDirForSlug(slug)

	if dirty, err := gitWorkingTreeDirty(ctx, cloneRoot); err != nil {
		return nil, nil, err
	} else if dirty {
		return nil, nil, fmt.Errorf("sessions working tree at %s is dirty", cloneRoot)
	}

	proposalID, err := NewID()
	if err != nil {
		return nil, nil, fmt.Errorf("new proposal id: %w", err)
	}
	if err := os.MkdirAll(proposalsPath, 0o700); err != nil {
		return nil, nil, fmt.Errorf("mkdir proposals: %w", err)
	}
	proposalPath := filepath.Join(proposalsPath, proposalID+".md")
	if err := drafter(ctx,
		filepath.Join(sessionDir, fileMetadata),
		filepath.Join(sessionDir, "events.jsonl"),
		proposalPath,
	); err != nil {
		return nil, nil, fmt.Errorf("drafter: %w", err)
	}
	draftBytes, err := os.ReadFile(proposalPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read draft: %w", err)
	}

	fmBytes, body, err := wikiSplitFrontmatter(draftBytes)
	if err != nil {
		return nil, []string{"draft missing valid frontmatter: " + err.Error()}, nil
	}
	var fm sessionFrontmatter
	if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
		return nil, []string{"frontmatter parse: " + err.Error()}, nil
	}

	// Overlay deterministic fields from metadata so the AI cannot hallucinate
	// identity-bearing values.
	fm.SchemaVersion = sessionsCurrentSchemaVersion
	fm.ID = slug
	fm.Date = createdAt.UTC().Format("2006-01-02")
	fm.Author = sessionAuthor{Name: meta.Author.Name, Email: meta.Author.Email}
	if fm.Author.Name == "" {
		// Pre-existing session migration: resolve at push time.
		a := resolveGitAuthor()
		fm.Author = sessionAuthor(a)
		meta.Author = a
		if err := writePersistedMetadata(sessionDir, meta); err != nil {
			return nil, nil, fmt.Errorf("backfill author: %w", err)
		}
	}
	fm.Namespace = meta.Namespace
	touched, _ := ContextsTouched(sessionDir)
	names := make([]string, 0, len(touched))
	for _, t := range touched {
		names = append(names, t.Name)
	}
	fm.ClustersTouched = names
	fm.Sources = sessionSources{
		IncidentIO: meta.IncidentURL,
		Slack:      meta.SlackChannelURL,
		Bundle:     "session.triagent.json",
	}
	if errs := validateSessionFrontmatter(fm); len(errs) > 0 {
		return nil, errs, nil
	}
	if errs := validateSessionBodyHeaders(string(body)); len(errs) > 0 {
		return nil, errs, nil
	}

	fmEncoded, err := yaml.Marshal(fm)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal frontmatter: %w", err)
	}
	finalMD := append([]byte("---\n"), fmEncoded...)
	finalMD = append(finalMD, []byte("---\n\n")...)
	finalMD = append(finalMD, body...)

	bundle, err := buildShareBundle(sessionDir)
	if err != nil {
		return nil, nil, fmt.Errorf("build bundle: %w", err)
	}
	bundleJSON, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("marshal bundle: %w", err)
	}

	base := strings.TrimSpace(req.Base)
	if base == "" {
		base = "main"
	}
	branch := strings.TrimSpace(req.Branch)
	if branch == "" {
		branch = "sessions/" + slug
	}
	if !branchSafe.MatchString(branch) {
		return nil, nil, fmt.Errorf("branch name %q has unsupported characters", branch)
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = fmt.Sprintf("session: %s — %s", slug, fm.Title)
	}
	prBody := strings.TrimSpace(req.Body)
	if prBody == "" {
		prBody = fmt.Sprintf("Pushes investigation session `%s` from the triagent launcher.", slug)
	}

	if out, err := runGit(ctx, cloneRoot, "fetch", "origin", base); err != nil {
		return nil, nil, fmt.Errorf("git fetch origin %s: %w (%s)", base, err, out)
	}
	if out, err := runGit(ctx, cloneRoot, "checkout", "-B", branch, "origin/"+base); err != nil {
		return nil, nil, fmt.Errorf("git checkout: %w (%s)", err, out)
	}

	// Per-session dir lives at `<sessionsPath>/<monthDir>/<slug>/`. On
	// the default profile (sessions_path=sessions) the in-repo path is
	// `sessions/<monthDir>/<slug>/`, matching the legacy layout.
	sessionAbs := filepath.Join(sessionsPath, monthDir, slug)
	if err := os.MkdirAll(sessionAbs, 0o755); err != nil {
		return nil, nil, fmt.Errorf("mkdir session: %w", err)
	}
	if err := os.WriteFile(filepath.Join(sessionAbs, "session.md"), finalMD, 0o644); err != nil {
		return nil, nil, fmt.Errorf("write session.md: %w", err)
	}
	if err := os.WriteFile(filepath.Join(sessionAbs, "session.triagent.json"), bundleJSON, 0o644); err != nil {
		return nil, nil, fmt.Errorf("write bundle: %w", err)
	}
	if out, err := runGit(ctx, cloneRoot, "add", "-A"); err != nil {
		return nil, nil, fmt.Errorf("git add: %w (%s)", err, out)
	}
	if changed, err := gitHasStagedChanges(ctx, cloneRoot); err != nil {
		return nil, nil, err
	} else if !changed {
		return nil, []string{"no changes to commit — session already in repo at " + base}, nil
	}
	commitMsg := fmt.Sprintf("session: %s — %s", slug, fm.Title)
	if out, err := runGit(ctx, cloneRoot, "commit", "-m", commitMsg); err != nil {
		return nil, nil, fmt.Errorf("git commit: %w (%s)", err, out)
	}
	// --force-with-lease + best-effort fetch — see repo_pr.go for the
	// rationale (re-push of the same branch updates the PR).
	_, _ = runGit(ctx, cloneRoot, "fetch", "origin", branch)
	if out, err := runGit(ctx, cloneRoot, "push", "--force-with-lease", "-u", "origin", branch); err != nil {
		return nil, nil, fmt.Errorf("git push: %w (%s)", err, out)
	}

	url, err := openGHPullRequest(ctx, cloneRoot, upstreamRepo, base, branch, title, prBody)
	if err != nil {
		return nil, nil, err
	}
	_ = os.Remove(proposalPath)

	return &sessionPushResult{
		URL:    url,
		Branch: branch,
		Base:   base,
		Slug:   slug,
	}, nil, nil
}
