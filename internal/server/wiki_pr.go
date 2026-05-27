package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// pushWikiPRRequest is what the approve and push-pr handlers forward into
// pushWikiToVault / pushWikiPR. ProposalID is used by the approve flow;
// Slug + Branch/Title/Body/Base are used by the push-pr flow.
// Hooks for operator-overridable branch/title/body are kept in the struct
// so defaults are derived from the slug and title when absent.
type pushWikiPRRequest struct {
	ProposalID string // approve flow: the proposal to promote
	Slug       string // PR flow: the already-committed entry slug
	Branch     string // optional override
	Title      string // optional override
	Body       string // optional override
	Base       string // target branch on origin; defaults to "main"
}

// pushWikiToVaultResult is the success payload from pushWikiToVault.
type pushWikiToVaultResult struct {
	Slug         string   `json:"slug"`
	Path         string   `json:"path"`
	Commit       string   `json:"commit"`
	StubsCreated []string `json:"stubs_created,omitempty"`
}

// pushWikiPRResult is the success payload from pushWikiPR.
type pushWikiPRResult struct {
	URL    string `json:"url"`
	Branch string `json:"branch"`
	Base   string `json:"base"`
	Slug   string `json:"slug"`
}

// pushWikiToVault writes the proposed entry into the local vault on
// the CURRENT branch and commits. No fetch, no branch switch, no push,
// no gh. Mirrors the playbook proposal approve flow: local-only.
//
// Refuses on a dirty wiki tree to avoid mixing operator work with the
// promotion. Returns the slug, path (relative to the clone root, so
// callers see the actual in-repo location), commit sha, and the entity
// stubs that were auto-created from [[wikilinks]].
//
// cloneRoot is the cwd for git commands; vaultPath is where the
// `entries/` + `entities/` trees live (= cloneRoot on flat layouts, or
// `cloneRoot/<wiki_path>` when the profile sets defaults.wiki_path).
func pushWikiToVault(
	ctx context.Context,
	caps Capabilities,
	cloneRoot string,
	vaultPath string,
	mcpBin string,
	proposalsPath string,
	req pushWikiPRRequest,
) (*pushWikiToVaultResult, []string, error) {
	if !caps.Wiki.Valid {
		return nil, nil, fmt.Errorf("wiki vault not ready: %s", caps.Wiki.Reason)
	}
	if !wikiProposalIDPattern.MatchString(req.ProposalID) {
		return nil, nil, fmt.Errorf("invalid wiki proposal id %q", req.ProposalID)
	}

	// Read the draft and any pre-populated entity stubs from the sub-agent.
	slug, draftBody, err := readWikiDraft(proposalsPath, req.ProposalID)
	if err != nil {
		return nil, nil, fmt.Errorf("read draft: %w", err)
	}
	prePopulatedStubs, stubParseErrs := readWikiNewEntityStubs(proposalsPath, req.ProposalID)
	if len(stubParseErrs) > 0 {
		// propose_wiki_draft also validates this; reaching it here means
		// the proposals dir was hand-edited (or a stub got corrupted)
		// after the draft was created. Refuse rather than silently
		// promoting a stub with an empty Description.
		errs := make([]string, 0, len(stubParseErrs))
		for _, e := range stubParseErrs {
			errs = append(errs, "entity stub parse failed: "+e)
		}
		return nil, errs, nil
	}
	if err := wikiValidateSlug(slug); err != nil {
		return nil, []string{err.Error()}, nil
	}

	fmBytes, body, err := wikiSplitFrontmatter([]byte(draftBody))
	if err != nil {
		return nil, []string{"draft missing valid frontmatter: " + err.Error()}, nil
	}
	var fm wikiFrontmatter
	if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
		return nil, []string{"frontmatter parse: " + err.Error()}, nil
	}
	if fm.ID != slug {
		return nil, []string{fmt.Sprintf("draft frontmatter id %q does not match filename id %q", fm.ID, slug)}, nil
	}
	if errs := wikiValidateFrontmatter(fm); len(errs) > 0 {
		return nil, errs, nil
	}
	if errs := wikiValidateBodyHeaders(string(body)); len(errs) > 0 {
		return nil, errs, nil
	}

	// Refuse on dirty tree to avoid mixing operator work with the promotion.
	if dirty, err := gitWorkingTreeDirty(ctx, cloneRoot); err != nil {
		return nil, nil, err
	} else if dirty {
		return nil, nil, fmt.Errorf("wiki working tree at %s is dirty — commit or stash before promoting", cloneRoot)
	}

	// Write the entry file at `<vaultPath>/entries/<slug>.md`.
	entryAbs := filepath.Join(vaultPath, "entries", slug+".md")
	entryRelToClone, err := filepath.Rel(cloneRoot, entryAbs)
	if err != nil {
		return nil, nil, fmt.Errorf("compute entry path relative to clone root: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(entryAbs), 0o700); err != nil {
		return nil, nil, fmt.Errorf("mkdir entries: %w", err)
	}
	if err := os.WriteFile(entryAbs, []byte(draftBody), 0o644); err != nil {
		return nil, nil, fmt.Errorf("write entry: %w", err)
	}

	// Create entity stubs from [[wikilinks]] in body, if not present.
	// Prefer pre-populated descriptions from the sub-agent; fall back to
	// empty stubs for any entities the sub-agent did not pre-stub.
	stubsCreated, err := createEntityStubs(vaultPath, fm, string(body), prePopulatedStubs)
	if err != nil {
		return nil, nil, err
	}

	if out, err := runGit(ctx, cloneRoot, "add", "-A"); err != nil {
		return nil, nil, fmt.Errorf("git add: %w (%s)", err, out)
	}
	if changed, err := gitHasStagedChanges(ctx, cloneRoot); err != nil {
		return nil, nil, err
	} else if !changed {
		return nil, []string{"no changes to commit — the wiki already contains this content"}, nil
	}

	commitMsg := fmt.Sprintf("wiki: %s — %s", slug, fm.Title)
	if out, err := runGit(ctx, cloneRoot, "commit", "-m", commitMsg); err != nil {
		return nil, nil, fmt.Errorf("git commit: %w (%s)", err, out)
	}

	// Read the commit sha.
	sha, err := runGit(ctx, cloneRoot, "rev-parse", "--short", "HEAD")
	if err != nil {
		sha = "unknown"
	}

	// Delete the local draft now that it's committed. Non-fatal.
	_ = deleteWikiProposalLocal(proposalsPath, req.ProposalID)

	return &pushWikiToVaultResult{
		Slug:         slug,
		Path:         entryRelToClone,
		Commit:       sha,
		StubsCreated: stubsCreated,
	}, nil, nil
}

// pushWikiPR drives the PR-creation flow for an ALREADY-COMMITTED
// entry in the vault. Triggered by the operator from the wiki entry
// detail view. Sequence:
//  1. Capability check (gh + wiki).
//  2. Read entry bytes from the local vault checkout.
//  3. Validate frontmatter / body headers.
//  4. Refuse on dirty tree (the launcher won't auto-stash).
//  5. git fetch origin <base>.
//  6. Create branch from origin/<base>.
//  7. Re-apply: write the entry file + entity stubs onto the fresh branch.
//  8. git push -u origin <branch>.
//  9. gh pr create.
func pushWikiPR(
	ctx context.Context,
	caps Capabilities,
	cloneRoot string,
	vaultPath string,
	upstreamRepo string,
	req pushWikiPRRequest,
) (*pushWikiPRResult, []string, error) {
	if upstreamRepo == "" {
		upstreamRepo = "sourcehawk/triagent-wiki"
	}
	if !caps.GH.Authenticated {
		return nil, nil, fmt.Errorf("gh CLI not ready: %s", caps.GH.Reason)
	}
	if !caps.Wiki.Valid {
		return nil, nil, fmt.Errorf("wiki vault not ready: %s", caps.Wiki.Reason)
	}
	if req.Slug == "" {
		return nil, nil, fmt.Errorf("slug is required")
	}
	if err := wikiValidateSlug(req.Slug); err != nil {
		return nil, nil, err
	}

	// Read the entry file from the local vault.
	entryAbs := filepath.Join(vaultPath, "entries", req.Slug+".md")
	entryBytes, err := os.ReadFile(entryAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("entry %s not found in vault (approve first)", req.Slug)
		}
		return nil, nil, fmt.Errorf("read entry: %w", err)
	}

	fmBytes, body, err := wikiSplitFrontmatter(entryBytes)
	if err != nil {
		return nil, []string{"entry missing valid frontmatter: " + err.Error()}, nil
	}
	var fm wikiFrontmatter
	if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
		return nil, []string{"frontmatter parse: " + err.Error()}, nil
	}
	if errs := wikiValidateFrontmatter(fm); len(errs) > 0 {
		return nil, errs, nil
	}
	if errs := wikiValidateBodyHeaders(string(body)); len(errs) > 0 {
		return nil, errs, nil
	}

	// Snapshot entity stubs that exist at current vault state for
	// re-application after branch switch.
	entityStubPaths, entityStubContents, err := snapshotEntityStubs(vaultPath, fm, string(body))
	if err != nil {
		return nil, nil, err
	}

	base := strings.TrimSpace(req.Base)
	if base == "" {
		base = "main"
	}
	branch := strings.TrimSpace(req.Branch)
	if branch == "" {
		branch = fmt.Sprintf("wiki/%s-%s", req.Slug, time.Now().UTC().Format("20060102"))
	}
	if !branchSafe.MatchString(branch) {
		return nil, nil, fmt.Errorf("branch name %q has unsupported characters (allowed: [A-Za-z0-9._/-])", branch)
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = fmt.Sprintf("wiki: %s — %s", req.Slug, fm.Title)
	}
	prBody := strings.TrimSpace(req.Body)
	if prBody == "" {
		prBody = fmt.Sprintf("Promotes wiki entry for `%s` from the triagent launcher.\n\n%s", req.Slug, fm.Title)
	}

	// Refuse on dirty tree.
	if dirty, err := gitWorkingTreeDirty(ctx, cloneRoot); err != nil {
		return nil, nil, err
	} else if dirty {
		return nil, nil, fmt.Errorf("wiki working tree at %s is dirty — commit or stash before pushing a PR", cloneRoot)
	}

	if out, err := runGit(ctx, cloneRoot, "fetch", "origin", base); err != nil {
		return nil, nil, fmt.Errorf("git fetch origin %s: %w (%s)", base, err, out)
	}
	if out, err := runGit(ctx, cloneRoot, "checkout", "-B", branch, "origin/"+base); err != nil {
		return nil, nil, fmt.Errorf("git checkout -B %s origin/%s: %w (%s)", branch, base, err, out)
	}

	// Re-apply the entry file onto the fresh branch.
	if err := os.MkdirAll(filepath.Dir(entryAbs), 0o700); err != nil {
		return nil, nil, fmt.Errorf("mkdir entries: %w", err)
	}
	if err := os.WriteFile(entryAbs, entryBytes, 0o644); err != nil {
		return nil, nil, fmt.Errorf("write entry: %w", err)
	}

	// Re-apply entity stubs that don't exist on the new branch.
	for i, stubAbs := range entityStubPaths {
		if _, err := os.Stat(stubAbs); err == nil {
			continue // already present on origin/<base>
		}
		if err := os.MkdirAll(filepath.Dir(stubAbs), 0o700); err != nil {
			return nil, nil, fmt.Errorf("mkdir stub dir: %w", err)
		}
		if err := os.WriteFile(stubAbs, entityStubContents[i], 0o644); err != nil {
			return nil, nil, fmt.Errorf("write stub: %w", err)
		}
	}

	if out, err := runGit(ctx, cloneRoot, "add", "-A"); err != nil {
		return nil, nil, fmt.Errorf("git add: %w (%s)", err, out)
	}
	if changed, err := gitHasStagedChanges(ctx, cloneRoot); err != nil {
		return nil, nil, err
	} else if !changed {
		return nil, []string{"no changes to commit — the wiki already contains this content on " + base}, nil
	}
	if out, err := runGit(ctx, cloneRoot, "commit", "-m", title); err != nil {
		return nil, nil, fmt.Errorf("git commit: %w (%s)", err, out)
	}

	// --force-with-lease lets a re-push of the same branch update the
	// existing PR; the best-effort fetch first gives the lease check
	// a baseline to enforce against. See the matching block in
	// repo_pr.go for the rationale.
	_, _ = runGit(ctx, cloneRoot, "fetch", "origin", branch)
	if out, err := runGit(ctx, cloneRoot, "push", "--force-with-lease", "-u", "origin", branch); err != nil {
		return nil, nil, fmt.Errorf("git push: %w (%s)", err, out)
	}

	url, err := openGHPullRequest(ctx, cloneRoot, upstreamRepo, base, branch, title, prBody)
	if err != nil {
		return nil, nil, err
	}

	return &pushWikiPRResult{
		URL:    url,
		Branch: branch,
		Base:   base,
		Slug:   req.Slug,
	}, nil, nil
}

// wikiFileOnOrigin reports whether the given vault-relative path exists
// on the upstream HEAD ref. Used by the wiki incident detail view to
// decide between "local-only delete" and "open a PR upstream to delete"
// at confirmation time.
//
// Returns false on any git error — without an upstream there's nothing
// to delete-via-PR, so the safe default is to fall back to local-only.
func wikiFileOnOrigin(ctx context.Context, vaultPath, vaultRel string) bool {
	if vaultPath == "" || vaultRel == "" {
		return false
	}
	// `git cat-file -e <ref>:<path>` exits 0 when the blob exists.
	if _, err := runGit(ctx, vaultPath, "cat-file", "-e", "origin/HEAD:"+vaultRel); err == nil {
		return true
	}
	// Fall back to origin/main for repos that don't expose origin/HEAD
	// as a symbolic ref (mirrors unsyncedWikiFiles).
	if _, err := runGit(ctx, vaultPath, "cat-file", "-e", "origin/main:"+vaultRel); err == nil {
		return true
	}
	return false
}

// pushWikiDeletePR opens a PR upstream that removes an entry file
// from the wiki repo, then deletes the file from the local vault on
// the base branch. Used when the operator clicks "delete" on a wiki
// entry that exists upstream — the local-only delete would otherwise
// strand the file on origin with no way to push the removal (the
// "after deletion the wiki is no longer visible for pushing" problem).
//
// Mirrors pushWikiPR's structure: capability check → fetch → side
// branch from origin/<base> → delete + commit + push → gh pr create.
// After the PR is open, switch back to <base> and apply the same
// delete locally so the operator's view is consistent.
func pushWikiDeletePR(
	ctx context.Context,
	caps Capabilities,
	cloneRoot string,
	vaultPath string,
	upstreamRepo string,
	req pushWikiPRRequest,
) (*pushWikiPRResult, []string, error) {
	if upstreamRepo == "" {
		upstreamRepo = "sourcehawk/triagent-wiki"
	}
	if !caps.GH.Authenticated {
		return nil, nil, fmt.Errorf("gh CLI not ready: %s", caps.GH.Reason)
	}
	if !caps.Wiki.Valid {
		return nil, nil, fmt.Errorf("wiki vault not ready: %s", caps.Wiki.Reason)
	}
	if err := wikiValidateSlug(req.Slug); err != nil {
		return nil, nil, err
	}

	entryAbs := filepath.Join(vaultPath, "entries", req.Slug+".md")
	// entryRelToClone is the path interpreted by `git add` — must be
	// relative to the cwd we run git in (cloneRoot).
	entryRelToClone, err := filepath.Rel(cloneRoot, entryAbs)
	if err != nil {
		return nil, nil, fmt.Errorf("compute entry path relative to clone root: %w", err)
	}

	// Snapshot the title from local for nicer PR defaults. Optional —
	// if the local file is missing, fall back to the slug.
	title := strings.TrimSpace(req.Title)
	if title == "" {
		if data, err := os.ReadFile(entryAbs); err == nil {
			if fmBytes, _, err := wikiSplitFrontmatter(data); err == nil {
				var fm wikiFrontmatter
				if yaml.Unmarshal(fmBytes, &fm) == nil && strings.TrimSpace(fm.Title) != "" {
					title = fmt.Sprintf("wiki: delete %s — %s", req.Slug, fm.Title)
				}
			}
		}
		if title == "" {
			title = "wiki: delete " + req.Slug
		}
	}

	base := strings.TrimSpace(req.Base)
	if base == "" {
		base = "main"
	}
	branch := strings.TrimSpace(req.Branch)
	if branch == "" {
		branch = fmt.Sprintf("wiki/delete-%s-%s", req.Slug, time.Now().UTC().Format("20060102"))
	}
	if !branchSafe.MatchString(branch) {
		return nil, nil, fmt.Errorf("branch name %q has unsupported characters (allowed: [A-Za-z0-9._/-])", branch)
	}
	prBody := strings.TrimSpace(req.Body)
	if prBody == "" {
		prBody = fmt.Sprintf("Removes wiki entry `%s` from the triagent launcher.", req.Slug)
	}

	if dirty, err := gitWorkingTreeDirty(ctx, cloneRoot); err != nil {
		return nil, nil, err
	} else if dirty {
		return nil, nil, fmt.Errorf("wiki working tree at %s is dirty — commit or stash before deleting", cloneRoot)
	}

	if out, err := runGit(ctx, cloneRoot, "fetch", "origin", base); err != nil {
		return nil, nil, fmt.Errorf("git fetch origin %s: %w (%s)", base, err, out)
	}
	if out, err := runGit(ctx, cloneRoot, "checkout", "-B", branch, "origin/"+base); err != nil {
		return nil, nil, fmt.Errorf("git checkout -B %s origin/%s: %w (%s)", branch, base, err, out)
	}

	// On the side branch (= origin/<base>), the file should exist. If
	// it doesn't, there's nothing to PR-delete and we bail out so the
	// caller can fall back to the local-only delete path.
	if _, err := os.Stat(entryAbs); err != nil {
		if os.IsNotExist(err) {
			// Restore base branch so the launcher's main view is sane.
			_, _ = runGit(ctx, cloneRoot, "checkout", base)
			return nil, []string{"entry is not on " + base + " upstream — nothing to delete via PR"}, nil
		}
		return nil, nil, fmt.Errorf("stat entry on side branch: %w", err)
	}

	if err := os.Remove(entryAbs); err != nil {
		return nil, nil, fmt.Errorf("remove entry on side branch: %w", err)
	}
	if out, err := runGit(ctx, cloneRoot, "add", "-A", entryRelToClone); err != nil {
		return nil, nil, fmt.Errorf("git add: %w (%s)", err, out)
	}
	if changed, err := gitHasStagedChanges(ctx, cloneRoot); err != nil {
		return nil, nil, err
	} else if !changed {
		return nil, []string{"no changes to commit — the entry is already absent on " + base}, nil
	}
	if out, err := runGit(ctx, cloneRoot, "commit", "-m", title); err != nil {
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

	// Apply the deletion to the base branch too so the operator's
	// local view stops showing the entry. Best-effort: if checkout
	// fails the PR is still open and we surface the partial state.
	if out, err := runGit(ctx, cloneRoot, "checkout", base); err != nil {
		return &pushWikiPRResult{URL: url, Branch: branch, Base: base, Slug: req.Slug}, nil,
			fmt.Errorf("PR opened (%s) but failed to switch back to %s: %w (%s)", url, base, err, out)
	}
	if _, err := os.Stat(entryAbs); err == nil {
		if err := os.Remove(entryAbs); err != nil {
			return &pushWikiPRResult{URL: url, Branch: branch, Base: base, Slug: req.Slug}, nil,
				fmt.Errorf("PR opened (%s) but failed to remove local file: %w", url, err)
		}
		if out, err := runGit(ctx, cloneRoot, "add", "-A", entryRelToClone); err != nil {
			return &pushWikiPRResult{URL: url, Branch: branch, Base: base, Slug: req.Slug}, nil,
				fmt.Errorf("PR opened (%s) but local git add failed: %w (%s)", url, err, out)
		}
		commitMsg := "wiki: delete " + req.Slug + " (PR " + url + ")"
		if out, err := runGit(ctx, cloneRoot, "commit", "-m", commitMsg); err != nil {
			return &pushWikiPRResult{URL: url, Branch: branch, Base: base, Slug: req.Slug}, nil,
				fmt.Errorf("PR opened (%s) but local git commit failed: %w (%s)", url, err, out)
		}
	}

	return &pushWikiPRResult{
		URL:    url,
		Branch: branch,
		Base:   base,
		Slug:   req.Slug,
	}, nil, nil
}

// snapshotEntityStubs reads the entity stubs referenced by [[wikilinks]]
// in body that currently exist in the vault. Returns parallel slices of
// abs path + file content so they can be re-applied after a branch switch.
func snapshotEntityStubs(vaultPath string, fm wikiFrontmatter, body string) (paths []string, contents [][]byte, err error) {
	matches := wikilinkPattern.FindAllStringSubmatch(body, -1)
	seen := make(map[string]bool)
	for _, m := range matches {
		name := m[1]
		if seen[name] {
			continue
		}
		seen[name] = true

		var dir string
		switch {
		case containsString(fm.Services, name):
			dir = "services"
		case containsString(fm.Errors, name):
			dir = "errors"
		case containsString(fm.Symptoms, name):
			dir = "symptoms"
		default:
			dir = "components"
		}

		stubAbs := filepath.Join(vaultPath, "entities", dir, name+".md")
		data, err := os.ReadFile(stubAbs)
		if err != nil {
			if os.IsNotExist(err) {
				continue // doesn't exist in vault yet; will be created at PR time
			}
			return nil, nil, fmt.Errorf("read stub %s: %w", stubAbs, err)
		}
		paths = append(paths, stubAbs)
		contents = append(contents, data)
	}
	return paths, contents, nil
}

// deleteWikiProposalLocal removes the proposal file from the proposals dir
// by scanning for the <proposalID>__*.md filename. Mirrors wiki.DeleteProposal
// without the subprocess hop — used for the post-approve cleanup inside pushWikiToVault.
func deleteWikiProposalLocal(dir, proposalID string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	prefix := proposalID + "__"
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		full := filepath.Join(dir, e.Name())
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// wikiSplitFrontmatter splits a markdown file with YAML frontmatter into
// (frontmatter bytes, body bytes). Mirrors mcp/internal/wiki.SplitFrontmatter
// inline so the investigate module doesn't need to depend on the mcp module.
func wikiSplitFrontmatter(raw []byte) (fm, body []byte, err error) {
	const sep = "---"
	s := string(raw)
	if !strings.HasPrefix(s, sep+"\n") && !strings.HasPrefix(s, sep+"\r\n") {
		return nil, nil, fmt.Errorf("no frontmatter delimiter at top of file")
	}
	rest := s[len(sep)+1:]
	end := strings.Index(rest, "\n"+sep+"\n")
	if end < 0 {
		end = strings.Index(rest, "\n"+sep+"\r\n")
	}
	if end < 0 {
		return nil, nil, fmt.Errorf("no closing frontmatter delimiter")
	}
	fm = []byte(rest[:end])
	bodyStart := end + len("\n"+sep+"\n")
	if bodyStart > len(rest) {
		bodyStart = len(rest)
	}
	body = []byte(rest[bodyStart:])
	return fm, body, nil
}

// wikiCurrentSchemaVersion mirrors mcp/internal/wiki.CurrentSchemaVersion.
// Bumped together when the wiki schema breaks.
const wikiCurrentSchemaVersion = 1

// wikiFrontmatter is the YAML frontmatter shape for an incident note.
// Mirrors mcp/internal/wiki.Frontmatter inline.
type wikiFrontmatter struct {
	SchemaVersion int      `yaml:"schema_version,omitempty"`
	ID            string   `yaml:"id"`
	Date          string   `yaml:"date"`
	Title         string   `yaml:"title"`
	Status        string   `yaml:"status"`
	Severity      string   `yaml:"severity,omitempty"`
	Services      []string `yaml:"services"`
	Errors        []string `yaml:"errors"`
	Symptoms      []string `yaml:"symptoms"`
	Links         struct {
		Investigation string `yaml:"investigation,omitempty"`
		IncidentIO    string `yaml:"incident_io,omitempty"`
		SlackChannel  string `yaml:"slack_channel,omitempty"`
		SlackMessage  string `yaml:"slack_message,omitempty"`
	} `yaml:"links,omitempty"`
}

var wikiSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// wikiValidateSlug rejects empty / malformed slugs. The format guidance
// (`inc-` / `inv-` / `alert-` prefixes) lives in the agent prompt, not
// the validator.
func wikiValidateSlug(slug string) error {
	if slug == "" {
		return fmt.Errorf("slug is required")
	}
	if !wikiSlugPattern.MatchString(slug) {
		return fmt.Errorf("slug %q must be lowercase-with-hyphens (^[a-z0-9][a-z0-9-]*$)", slug)
	}
	if strings.HasSuffix(slug, "-") {
		return fmt.Errorf("slug %q has empty trailing segment", slug)
	}
	return nil
}

var wikiValidStatus = map[string]bool{"resolved": true, "open": true, "wontfix": true}
var wikiValidSeverity = map[string]bool{"": true, "sev1": true, "sev2": true, "sev3": true}

// wikiValidateFrontmatter returns a list of validation errors (empty when ok).
func wikiValidateFrontmatter(fm wikiFrontmatter) []string {
	var errs []string
	// schema_version 0 (absent) is treated as wikiCurrentSchemaVersion for
	// back-compat. Non-zero must match — otherwise the rest of the parsed
	// shape can't be trusted.
	if fm.SchemaVersion != 0 && fm.SchemaVersion != wikiCurrentSchemaVersion {
		errs = append(errs, fmt.Sprintf("schema_version %d unsupported; this build understands %d", fm.SchemaVersion, wikiCurrentSchemaVersion))
	}
	if err := wikiValidateSlug(fm.ID); err != nil {
		errs = append(errs, err.Error())
	}
	if strings.TrimSpace(fm.Date) == "" {
		errs = append(errs, "date is required (YYYY-MM-DD)")
	}
	if strings.TrimSpace(fm.Title) == "" {
		errs = append(errs, "title is required")
	}
	if !wikiValidStatus[fm.Status] {
		errs = append(errs, fmt.Sprintf("status %q invalid; want one of resolved|open|wontfix", fm.Status))
	}
	if !wikiValidSeverity[fm.Severity] {
		errs = append(errs, fmt.Sprintf("severity %q invalid; want one of sev1|sev2|sev3 (or omit)", fm.Severity))
	}
	if fm.Services == nil {
		errs = append(errs, "services array is required (may be empty)")
	}
	if fm.Errors == nil {
		errs = append(errs, "errors array is required (may be empty)")
	}
	if fm.Symptoms == nil {
		errs = append(errs, "symptoms array is required (may be empty)")
	}
	return errs
}

var wikiRequiredHeaders = []string{"## Summary", "## Root cause", "## Fix"}

// wikiValidateBodyHeaders checks that the markdown body has each required section.
func wikiValidateBodyHeaders(body string) []string {
	var errs []string
	for _, h := range wikiRequiredHeaders {
		if !strings.Contains(body, h) {
			errs = append(errs, fmt.Sprintf("missing required header %q", h))
		}
	}
	return errs
}

// createEntityStubs walks [[wikilinks]] in body, creates a stub at
// entities/<type>/<name>.md for any that don't yet exist. Type is inferred
// from frontmatter array membership (services / errors / symptoms); fallback
// is "components". When a pre-populated description is available from the
// sub-agent (passed via prePopulated), the stub ## Description section is
// filled in; otherwise the section is left empty as a placeholder.
// Returns vault-relative paths created.
var wikilinkPattern = regexp.MustCompile(`\[\[([a-z0-9][a-z0-9-]*)\]\]`)

func createEntityStubs(vaultPath string, fm wikiFrontmatter, body string, prePopulated []newEntityStub) ([]string, error) {
	matches := wikilinkPattern.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil, nil
	}

	// Build a lookup from (type, name) → description from the sub-agent stubs.
	// Also index by name only for a best-effort match when type diverges.
	stubByName := make(map[string]newEntityStub)
	for _, s := range prePopulated {
		stubByName[s.Name] = s
	}

	seen := make(map[string]bool)
	var created []string
	for _, m := range matches {
		name := m[1]
		if seen[name] {
			continue
		}
		seen[name] = true

		var dir, typ string
		switch {
		case containsString(fm.Services, name):
			dir, typ = "services", "service"
		case containsString(fm.Errors, name):
			dir, typ = "errors", "error"
		case containsString(fm.Symptoms, name):
			dir, typ = "symptoms", "symptom"
		default:
			dir, typ = "components", "component"
		}

		// If the sub-agent provided a stub for this name, prefer its type
		// classification (it may know better than the frontmatter arrays).
		if preStub, ok := stubByName[name]; ok && preStub.Type != "" {
			switch preStub.Type {
			case "service":
				dir = "services"
				typ = "service"
			case "error":
				dir = "errors"
				typ = "error"
			case "symptom":
				dir = "symptoms"
				typ = "symptom"
			case "component":
				dir = "components"
				typ = "component"
			}
		}

		stubRel := filepath.Join("entities", dir, name+".md")
		stubAbs := filepath.Join(vaultPath, stubRel)
		if _, err := os.Stat(stubAbs); err == nil {
			continue // already exists
		}
		if err := os.MkdirAll(filepath.Dir(stubAbs), 0o700); err != nil {
			return nil, fmt.Errorf("mkdir stub dir: %w", err)
		}

		// Build the stub content. Use the sub-agent's description if available.
		description := ""
		if preStub, ok := stubByName[name]; ok {
			description = preStub.Description
		}
		var stubContent string
		if description != "" {
			stubContent = fmt.Sprintf("---\nschema_version: %d\ntype: %s\nname: %s\ncreated: %s\n---\n\n## Description\n\n%s\n\n## Notes\n\n## Incidents\n",
				wikiCurrentSchemaVersion,
				typ, name, time.Now().UTC().Format("2006-01-02"), description)
		} else {
			stubContent = fmt.Sprintf("---\nschema_version: %d\ntype: %s\nname: %s\ncreated: %s\n---\n\n## Description\n\n## Notes\n\n## Incidents\n",
				wikiCurrentSchemaVersion,
				typ, name, time.Now().UTC().Format("2006-01-02"))
		}
		if err := os.WriteFile(stubAbs, []byte(stubContent), 0o644); err != nil {
			return nil, fmt.Errorf("write stub %s: %w", stubRel, err)
		}
		created = append(created, stubRel)
	}
	return created, nil
}

// containsString reports whether needle appears in haystack.
func containsString(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// wikiProposalIDPattern: same shape that triagent-mcp emits for wiki proposals.
var wikiProposalIDPattern = regexp.MustCompile(`^prop-[0-9a-f]{12}$`)
