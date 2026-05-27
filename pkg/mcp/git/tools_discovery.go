package git

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ── tool inputs ──────────────────────────────────────────────────────────────

type latestTagsIn struct {
	Limit              int  `json:"limit,omitempty" jsonschema:"max number of tags to return (default 10)"`
	IncludePrereleases bool `json:"include_prereleases,omitempty" jsonschema:"set true to include semver prerelease tags like 1.2.3-rc1 / 1.2.3-mytesttag (filtered by default). Tags ending in -SNAPSHOT and non-semver-shaped tags pass through regardless."`
}

type commitSummaryIn struct {
	Ref string `json:"ref" jsonschema:"git ref to summarise (commit sha, tag, or branch)"`
}

type diffSummaryIn struct {
	From string `json:"from" jsonschema:"earlier ref (commit/tag/branch)"`
	To   string `json:"to" jsonschema:"later ref (commit/tag/branch)"`
}

type searchLogIn struct {
	Grep  string   `json:"grep" jsonschema:"pattern to grep for in commit messages (regex; passed to git log --grep)"`
	Since string   `json:"since,omitempty" jsonschema:"git --since value (default '6 months')"`
	Paths []string `json:"paths,omitempty" jsonschema:"optional path filters; if set, only matches touching these paths are returned"`
}

// ── tool outputs ─────────────────────────────────────────────────────────────

type tagInfo struct {
	Name     string `json:"name"`
	Date     string `json:"date"`
	SHA      string `json:"sha"`
	Subject  string `json:"subject"`
}

type latestTagsOut struct {
	Repo string    `json:"repo"`
	Tags []tagInfo `json:"tags"`
}

type fileEntry struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

type commitStats struct {
	Files       int `json:"files"`
	Insertions  int `json:"insertions"`
	Deletions   int `json:"deletions"`
}

type commitSummaryOut struct {
	Repo string `json:"repo"`
	Ref  string `json:"ref"`
	// ResolvedRef is what `git` was actually invoked against. Differs
	// from Ref when the agent passed a bare branch name we rewrote to
	// origin/<name> for currency; equals Ref for tags / SHAs / qualified
	// refs.
	ResolvedRef string      `json:"resolved_ref,omitempty"`
	SHA         string      `json:"sha"`
	Author      string      `json:"author"`
	Date        string      `json:"date"`
	Subject     string      `json:"subject"`
	Body        string      `json:"body,omitempty"`
	Stats       commitStats `json:"stats"`
	Files       []fileEntry `json:"files"`
}

type commitRef struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
}

type diffFile struct {
	Path       string `json:"path"`
	Insertions int    `json:"insertions"`
	Deletions  int    `json:"deletions"`
}

type diffSummaryOut struct {
	Repo string `json:"repo"`
	From string `json:"from"`
	To   string `json:"to"`
	// ResolvedFrom / ResolvedTo are what `git diff` and `git log` were
	// actually invoked against — bare branch names get rewritten to
	// origin/<name> for currency.
	ResolvedFrom string      `json:"resolved_from,omitempty"`
	ResolvedTo   string      `json:"resolved_to,omitempty"`
	Commits      []commitRef `json:"commits"`
	Stats        commitStats `json:"stats"`
	TopFiles     []diffFile  `json:"top_files"`
}

type logMatch struct {
	SHA     string `json:"sha"`
	Date    string `json:"date"`
	Subject string `json:"subject"`
}

type searchLogOut struct {
	Repo    string     `json:"repo"`
	Grep    string     `json:"grep"`
	Since   string     `json:"since"`
	Matches []logMatch `json:"matches"`
}

// ── handlers ─────────────────────────────────────────────────────────────────

func (s *Server) latestTags(ctx context.Context, _ *mcp.CallToolRequest, in latestTagsIn) (*mcp.CallToolResult, latestTagsOut, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 10
	}
	dir, err := s.EnsureClone(ctx)
	if err != nil {
		return errorResult(err.Error()), emptyTags(s), nil
	}
	// Format: <name>|<isodate>|<commitSHA>|<commitSubject>|<tagAnnotation>
	// Sort by the underlying commit's date so annotated/lightweight tags
	// land in a consistent order even when the tag-creation timestamps
	// are close together. *committerdate falls back to committerdate for
	// lightweight tags.
	out, err := gitOutput(ctx, dir,
		"for-each-ref", "refs/tags/",
		"--sort=-*committerdate", "--sort=-committerdate",
		"--format=%(refname:short)|%(*committerdate:iso-strict)%(committerdate:iso-strict)|%(*objectname)%(objectname)|%(*subject)|%(subject)",
	)
	if err != nil {
		return errorResult(err.Error()), emptyTags(s), nil
	}
	filterEnabled := s.filterPrereleases && !in.IncludePrereleases
	tags := make([]tagInfo, 0, limit)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 5)
		if len(parts) < 5 {
			continue
		}
		if filterEnabled && isFilteredPrereleaseTag(parts[0]) {
			continue
		}
		// Prefer the dereferenced commit subject; fall back to the tag
		// annotation (lightweight tags don't have an annotation, so
		// *subject is empty and we show the commit subject from subject).
		subject := parts[3]
		if subject == "" {
			subject = parts[4]
		}
		tags = append(tags, tagInfo{
			Name:    parts[0],
			Date:    parts[1],
			SHA:     parts[2],
			Subject: subject,
		})
		if len(tags) >= limit {
			break
		}
	}
	return nil, latestTagsOut{Repo: s.repoFull(), Tags: tags}, nil
}

func (s *Server) commitSummary(ctx context.Context, _ *mcp.CallToolRequest, in commitSummaryIn) (*mcp.CallToolResult, commitSummaryOut, error) {
	if in.Ref == "" {
		return errorResult("ref is required"), emptyCommitSummary(s), nil
	}
	dir, err := s.EnsureClone(ctx)
	if err != nil {
		return errorResult(err.Error()), emptyCommitSummary(s), nil
	}

	// Rewrite bare branch refs to origin/<name> so we read the fresh tip
	// instead of the stale local branch the cache never fast-forwards.
	// Tags and SHAs pass through.
	resolvedRef, err := s.resolveRef(ctx, dir, in.Ref)
	if err != nil {
		return errorResult(err.Error()), emptyCommitSummary(s), nil
	}

	// Dereference annotated tags to their commit so subsequent commands
	// always operate on the commit object (otherwise `git show` on an
	// annotated tag mixes tag headers into the output).
	commitRefRaw, err := gitOutput(ctx, dir, "rev-parse", resolvedRef+"^{commit}")
	if err != nil {
		return errorResult(err.Error()), emptyCommitSummary(s), nil
	}
	commitRef := strings.TrimSpace(commitRefRaw)

	// Header: <sha>|<author>|<isodate>|<subject>%n<body...>
	headerOut, err := gitOutput(ctx, dir, "log", "-1", "--format=%H|%an <%ae>|%aI|%s%n%b", commitRef)
	if err != nil {
		return errorResult(err.Error()), emptyCommitSummary(s), nil
	}
	headerLine, body, _ := strings.Cut(strings.TrimRight(headerOut, "\n"), "\n")
	parts := strings.SplitN(headerLine, "|", 4)
	if len(parts) < 4 {
		return errorResult(fmt.Sprintf("unexpected git log output: %s", headerLine)), emptyCommitSummary(s), nil
	}

	// Files with status: git show --name-status (one per line: M\tpath, A\tpath...)
	filesOut, err := gitOutput(ctx, dir, "show", "--name-status", "--format=", commitRef)
	if err != nil {
		return errorResult(err.Error()), emptyCommitSummary(s), nil
	}
	files := make([]fileEntry, 0)
	for _, line := range strings.Split(strings.TrimSpace(filesOut), "\n") {
		if line == "" {
			continue
		}
		fparts := strings.SplitN(line, "\t", 2)
		if len(fparts) < 2 {
			continue
		}
		files = append(files, fileEntry{Status: fparts[0], Path: fparts[1]})
	}

	// Stats: git show --shortstat (e.g. " 3 files changed, 12 insertions(+), 5 deletions(-)")
	statsOut, err := gitOutput(ctx, dir, "show", "--shortstat", "--format=", commitRef)
	if err != nil {
		return errorResult(err.Error()), emptyCommitSummary(s), nil
	}
	stats := parseShortstat(statsOut)

	return nil, commitSummaryOut{
		Repo:        s.repoFull(),
		Ref:         in.Ref,
		ResolvedRef: resolvedRef,
		SHA:         parts[0],
		Author:      parts[1],
		Date:        parts[2],
		Subject:     parts[3],
		Body:        strings.TrimSpace(body),
		Stats:       stats,
		Files:       files,
	}, nil
}

func (s *Server) diffSummary(ctx context.Context, _ *mcp.CallToolRequest, in diffSummaryIn) (*mcp.CallToolResult, diffSummaryOut, error) {
	if in.From == "" || in.To == "" {
		return errorResult("from and to are required"), emptyDiffSummary(s, in), nil
	}
	dir, err := s.EnsureClone(ctx)
	if err != nil {
		return errorResult(err.Error()), emptyDiffSummary(s, in), nil
	}

	// Resolve both ends so bare branch names get the fresh tip, not the
	// stale local ref. Tags and SHAs pass through.
	resolvedFrom, err := s.resolveRef(ctx, dir, in.From)
	if err != nil {
		return errorResult(err.Error()), emptyDiffSummary(s, in), nil
	}
	resolvedTo, err := s.resolveRef(ctx, dir, in.To)
	if err != nil {
		return errorResult(err.Error()), emptyDiffSummary(s, in), nil
	}

	// Commit list
	logOut, err := gitOutput(ctx, dir, "log", "--format=%H|%s", fmt.Sprintf("%s..%s", resolvedFrom, resolvedTo))
	if err != nil {
		return errorResult(err.Error()), emptyDiffSummary(s, in), nil
	}
	commits := make([]commitRef, 0)
	for _, line := range strings.Split(strings.TrimSpace(logOut), "\n") {
		if line == "" {
			continue
		}
		cparts := strings.SplitN(line, "|", 2)
		if len(cparts) < 2 {
			continue
		}
		commits = append(commits, commitRef{SHA: cparts[0], Subject: cparts[1]})
	}

	// Aggregate stats
	statsOut, err := gitOutput(ctx, dir, "diff", "--shortstat", resolvedFrom, resolvedTo)
	if err != nil {
		return errorResult(err.Error()), emptyDiffSummary(s, in), nil
	}
	stats := parseShortstat(statsOut)

	// Top files by churn (insertions+deletions)
	numstatOut, err := gitOutput(ctx, dir, "diff", "--numstat", resolvedFrom, resolvedTo)
	if err != nil {
		return errorResult(err.Error()), emptyDiffSummary(s, in), nil
	}
	topFiles := parseNumstat(numstatOut, 20)

	return nil, diffSummaryOut{
		Repo:         s.repoFull(),
		From:         in.From,
		To:           in.To,
		ResolvedFrom: resolvedFrom,
		ResolvedTo:   resolvedTo,
		Commits:      commits,
		Stats:        stats,
		TopFiles:     topFiles,
	}, nil
}

func (s *Server) searchLog(ctx context.Context, _ *mcp.CallToolRequest, in searchLogIn) (*mcp.CallToolResult, searchLogOut, error) {
	if in.Grep == "" {
		return errorResult("grep is required"), searchLogOut{Repo: s.repoFull(), Matches: []logMatch{}}, nil
	}
	since := in.Since
	if since == "" {
		since = "6 months"
	}
	dir, err := s.EnsureClone(ctx)
	if err != nil {
		return errorResult(err.Error()), searchLogOut{Repo: s.repoFull(), Grep: in.Grep, Since: since, Matches: []logMatch{}}, nil
	}

	args := []string{
		"log",
		"--all",
		"--grep=" + in.Grep,
		"--since=" + since,
		"--format=%H|%aI|%s",
	}
	if len(in.Paths) > 0 {
		args = append(args, "--")
		args = append(args, in.Paths...)
	}
	out, err := gitOutput(ctx, dir, args...)
	if err != nil {
		return errorResult(err.Error()), searchLogOut{Repo: s.repoFull(), Grep: in.Grep, Since: since, Matches: []logMatch{}}, nil
	}
	matches := make([]logMatch, 0)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		mparts := strings.SplitN(line, "|", 3)
		if len(mparts) < 3 {
			continue
		}
		matches = append(matches, logMatch{SHA: mparts[0], Date: mparts[1], Subject: mparts[2]})
	}
	return nil, searchLogOut{Repo: s.repoFull(), Grep: in.Grep, Since: since, Matches: matches}, nil
}

// ── helpers ─────────────────────────────────────────────────────────────────

func (s *Server) repoFull() string { return s.owner + "/" + s.name }

func emptyTags(s *Server) latestTagsOut {
	return latestTagsOut{Repo: s.repoFull(), Tags: []tagInfo{}}
}

// semverCorePattern matches a bare MAJOR.MINOR.PATCH triple (no prerelease,
// no build metadata). Used by isFilteredPrereleaseTag to recognise the
// "semver-shaped" subset of tags — non-semver tags like `nightly-20260101`
// don't get filtered.
var semverCorePattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// isFilteredPrereleaseTag reports whether a tag is a semver-shaped
// prerelease build that the default filter hides. Strips an optional
// leading `v`, splits on the first `-`, and returns true when:
//
//   - the part before `-` is `MAJOR.MINOR.PATCH`
//   - the part after `-` is anything other than `SNAPSHOT`
//     (case-insensitive — Maven-style
//     `-SNAPSHOT` suffix and are kept as the stable "bleeding edge"
//     reference).
//
// Returns false for non-semver tags (`nightly-20260101`, `release-2026-05`),
// for tags with no prerelease suffix (`1.2.3`, `v8.7.0`), and for the
// SNAPSHOT exception. Build metadata after `+` is treated as part of
// the prerelease string for filtering purposes — matching the agent's
// likely intent ("tags that aren't shipped releases").
func isFilteredPrereleaseTag(tag string) bool {
	s := strings.TrimPrefix(tag, "v")
	dash := strings.Index(s, "-")
	if dash < 0 {
		return false
	}
	core := s[:dash]
	pre := s[dash+1:]
	if !semverCorePattern.MatchString(core) {
		return false
	}
	if strings.EqualFold(pre, "SNAPSHOT") {
		return false
	}
	return true
}

func emptyCommitSummary(s *Server) commitSummaryOut {
	return commitSummaryOut{Repo: s.repoFull(), Files: []fileEntry{}}
}

func emptyDiffSummary(s *Server, in diffSummaryIn) diffSummaryOut {
	return diffSummaryOut{Repo: s.repoFull(), From: in.From, To: in.To, Commits: []commitRef{}, TopFiles: []diffFile{}}
}

// parseShortstat parses git's "--shortstat" trailing line, e.g.
// " 3 files changed, 12 insertions(+), 5 deletions(-)". Missing fields
// (e.g. no insertions) parse to zero.
func parseShortstat(s string) commitStats {
	var stats commitStats
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			n, rest, ok := strings.Cut(p, " ")
			if !ok {
				continue
			}
			val, err := strconv.Atoi(n)
			if err != nil {
				continue
			}
			switch {
			case strings.Contains(rest, "file"):
				stats.Files = val
			case strings.Contains(rest, "insertion"):
				stats.Insertions = val
			case strings.Contains(rest, "deletion"):
				stats.Deletions = val
			}
		}
	}
	return stats
}

// parseNumstat parses `git diff --numstat` lines (`<ins>\t<del>\t<path>`)
// and returns the top N by total churn.
func parseNumstat(s string, top int) []diffFile {
	type entry struct {
		path     string
		ins, del int
	}
	all := make([]entry, 0)
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		ins, _ := strconv.Atoi(parts[0])
		del, _ := strconv.Atoi(parts[1])
		all = append(all, entry{path: parts[2], ins: ins, del: del})
	}
	// Insertion sort by churn desc; small N, keep it simple.
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && (all[j-1].ins+all[j-1].del) < (all[j].ins+all[j].del); j-- {
			all[j-1], all[j] = all[j], all[j-1]
		}
	}
	if len(all) > top {
		all = all[:top]
	}
	out := make([]diffFile, 0, len(all))
	for _, e := range all {
		out = append(out, diffFile{Path: e.path, Insertions: e.ins, Deletions: e.del})
	}
	return out
}
