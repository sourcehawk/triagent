package server

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/log"
)

// osStat is wrapped so tests can swap in a fake filesystem; production
// just delegates to os.Stat.
var osStat = os.Stat

// Capabilities is the launcher's view of optional dev tools the operator
// has on this machine. Filled at server startup, surfaced to the
// frontend via /api/capabilities so feature buttons can light up only
// when their underlying tool is ready.
//
// Today this covers `gh` and the playbooks repo path the PR-push flow
// writes into. New entries should follow the same shape: a Ready bit,
// optional details, and a single human-readable Reason set when
// something's off.
type Capabilities struct {
	GH       GHStatus          `json:"gh"`
	RepoPath RepoPathStatus     `json:"repoPath"`
	Wiki     WikiStatus         `json:"wiki"`
	Claude   ClaudeStatus       `json:"claude"`
	Sessions SessionsStatus     `json:"sessions"`
	Codefix  CodefixCapability  `json:"codefix"`
}

// CodefixCapability is the launcher's view of whether the
// "request codefix" surface is usable. Both GhAuthenticated AND
// HasLinkedRepos must be true. Reason is set when something's off
// (single human-readable line; the frontend renders it as the
// button's disabled-state tooltip).
type CodefixCapability struct {
	Enabled         bool   `json:"enabled"`
	HasLinkedRepos  bool   `json:"hasLinkedRepos"`
	GhAuthenticated bool   `json:"ghAuthenticated"`
	Reason          string `json:"reason,omitempty"`
}

// computeCodefixCapability folds the two inputs into the surfaced
// struct, including the reason precedence (gh first, repos second).
func computeCodefixCapability(ghAuthed, hasLinkedRepos bool) CodefixCapability {
	out := CodefixCapability{
		GhAuthenticated: ghAuthed,
		HasLinkedRepos:  hasLinkedRepos,
		Enabled:         ghAuthed && hasLinkedRepos,
	}
	if !out.Enabled {
		switch {
		case !ghAuthed:
			out.Reason = "GitHub CLI not authenticated — run `gh auth login` at launcher"
		case !hasLinkedRepos:
			out.Reason = "No repos linked to this investigation — add one in the Connections panel"
		}
	}
	return out
}

// SessionsStatus reports whether the launcher-managed sessions repo clone is
// configured and usable. Both Configured AND Valid must be true for
// the frontend to enable sessions upstream features (push PR, browse upstream).
// Mirrors WikiStatus exactly.
type SessionsStatus struct {
	Configured bool   `json:"configured"`     // profile's paths.upstream_sessions_dir is non-empty
	Valid      bool   `json:"valid"`          // exists, is dir, contains .git
	Path       string `json:"path,omitempty"`
	Repo       string `json:"repo,omitempty"` // upstream owner/name
	// Subpath is the in-repo dir under the upstream sessions repo where
	// per-month/per-slug session dirs live (profile's
	// defaults.sessions_path; "" = repo root).
	Subpath string `json:"subpath,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// ClaudeStatus reports whether the `claude` CLI is on PATH and runnable.
// We deliberately don't probe authentication — the only safe
// non-interactive verbs are `--version` / `--help` / `mcp list`; running
// bare `claude` would boot an interactive session inside the launcher
// process. Auth failures surface at session-start time with a clearer
// error than anything we'd synthesise here.
type ClaudeStatus struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// RepoPathStatus reports whether TRIAGENT_REPO_PATH (or
// --repo-path) points at a usable git checkout for playbook PRs.
type RepoPathStatus struct {
	Configured bool   `json:"configured"`           // operator set the path at all
	Valid      bool   `json:"valid"`                // path exists, is a directory, contains .git
	Path       string `json:"path,omitempty"`       // resolved absolute path
	// Repo is the upstream `owner/name` slug for the playbooks repo
	// (profile's defaults.playbooks_repo). Empty when the operator
	// hasn't pointed at an upstream — local-only mode. The frontend
	// gates push-as-PR affordances on this being non-empty: with no
	// remote, `gh pr create` has nowhere to push to.
	Repo string `json:"repo,omitempty"`
	// Subpath is the in-repo dir under the upstream playbooks repo
	// where playbook YAML lives (profile's defaults.playbooks_path;
	// "" = repo root). Mirrors WikiStatus.Subpath / SessionsStatus.Subpath
	// so the ancestry-walking .git probe and the frontend share the
	// same shape across the three repo-backed artifacts.
	Subpath string `json:"subpath,omitempty"`
	Reason  string `json:"reason,omitempty"`        // single-line explanation when something's off
}

// WikiStatus reports whether the launcher-managed wiki repo clone is
// configured and usable. Both Configured AND Valid must be true for
// the frontend to enable the "promote to wiki" button.
type WikiStatus struct {
	Configured bool   `json:"configured"`   // profile's paths.wiki_dir is non-empty
	Valid      bool   `json:"valid"`        // exists, is dir, contains .git
	Path       string `json:"path,omitempty"`
	Repo       string `json:"repo,omitempty"` // upstream owner/name
	// Subpath is the path inside the upstream repo where the wiki
	// `entries/` + `entities/` trees live (the profile's
	// defaults.wiki_path; "" means the repo root). Surfaced to the
	// frontend so the push-PR modal can show the actual in-repo
	// commit path when the wiki lives under a subdir of a shared
	// upstream.
	Subpath string `json:"subpath,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// GHStatus reports whether `gh` is installed AND authenticated. Both
// matter for opening PRs — installed-but-unauth'd is the trickier
// failure mode because the binary works but `gh pr create` will fail
// with a confusing 401.
type GHStatus struct {
	Installed     bool   `json:"installed"`
	Authenticated bool   `json:"authenticated"`
	Version       string `json:"version,omitempty"`     // e.g. "gh version 2.x"
	User          string `json:"user,omitempty"`        // GitHub username when authed
	Reason        string `json:"reason,omitempty"`      // single-line explanation when something's off
}

// checkCapabilities runs every capability probe. Each probe is bounded
// by its own short timeout so a stuck binary doesn't stall server
// startup.
//
// When repoPath is empty (operator didn't pass --repo-path / set
// $TRIAGENT_REPO_PATH) we try to auto-detect a playbooks repo checkout
// from the launcher's working directory before giving up. That covers
// the common dev case of running `triagent start` from inside the
// repo without any extra wiring.
//
// hasLinkedRepos should be true when at least one linked repo is
// configured (defaults + user + flags + env); it is used to gate the
// codefix surface together with gh authentication.
func checkCapabilities(ctx context.Context, repoPath, playbooksRepo, playbooksSubpath, wikiPath, wikiRepo, wikiSubpath, sessionsPath, sessionsRepo, sessionsSubpath string, hasLinkedRepos bool) Capabilities {
	if repoPath == "" {
		if detected := autoDetectRepoPath(ctx); detected != "" {
			repoPath = detected
		}
	}
	gh := checkGH(ctx)
	return Capabilities{
		GH:       gh,
		RepoPath: checkRepoPath(repoPath, playbooksRepo, playbooksSubpath),
		Wiki:     checkWiki(wikiPath, wikiRepo, wikiSubpath),
		Claude:   checkClaude(ctx),
		Sessions: checkSessions(sessionsPath, sessionsRepo, sessionsSubpath),
		Codefix:  computeCodefixCapability(gh.Authenticated, hasLinkedRepos),
	}
}

// checkClaude confirms `claude` is on PATH and `claude --version` exits
// cleanly. Bare `claude` would boot an interactive session, which we
// definitely don't want firing inside the launcher's startup path —
// `--version` is the standard non-interactive sanity check.
func checkClaude(ctx context.Context) ClaudeStatus {
	if _, err := exec.LookPath("claude"); err != nil {
		return ClaudeStatus{
			Reason: "claude CLI not found on PATH — every investigation session needs it. Install it from https://github.com/anthropics/claude-code or wherever your team distributes the binary.",
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, "claude", "--version").Output()
	if err != nil {
		return ClaudeStatus{
			Reason: "found claude on PATH but `claude --version` failed: " + err.Error(),
		}
	}
	version := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	return ClaudeStatus{Installed: true, Version: version}
}

// autoDetectRepoPath probes the launcher's CWD for a triagent checkout
// so operators developing inside the repo don't need to set --repo-path.
// Walks: CWD → `git rev-parse --show-toplevel` → check `origin` remote
// matches sourcehawk/triagent. Returns "" on any failure; the caller falls
// back to the unconfigured path and the PR-push button stays disabled.
func autoDetectRepoPath(ctx context.Context) string {
	if _, err := exec.LookPath("git"); err != nil {
		return ""
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	root, err := gitProbe(probeCtx, cwd, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return ""
	}

	origin, err := gitProbe(probeCtx, root, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	// Accept any URL form (https / ssh / git@): they all contain the
	// `<owner>/<repo>` pair somewhere.
	if !strings.Contains(origin, "sourcehawk/triagent") {
		return ""
	}
	return root
}

// gitProbe runs a git command and returns its trimmed stdout. Used by
// auto-detection where stderr noise from non-git directories would just
// clutter the log; bare exit-code suffices.
func gitProbe(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}

// checkRepoPath validates that the configured playbooks repo checkout is
// usable: directory exists and looks like a git repo. When subpath is
// non-empty, the work-dir is `<clone>/<subpath>` and the `.git` lives at
// the clone root — the probe walks ancestry to find it (same shape as
// checkWiki / checkSessions).
//
// We don't require it be the *correct* repo (right remote URL etc.) —
// that's a richer check we can layer on later if it matters in practice.
func checkRepoPath(path, playbooksRepo, subpath string) RepoPathStatus {
	if path == "" {
		return RepoPathStatus{
			Repo:    playbooksRepo,
			Subpath: subpath,
			Reason:  "no playbooks repo checkout detected. To enable playbook → PR push, either run `triagent start` from inside your playbooks repo checkout, pass --repo-path, or set $TRIAGENT_REPO_PATH.",
		}
	}
	info, err := osStat(path)
	if err != nil {
		return RepoPathStatus{
			Configured: true,
			Path:       path,
			Repo:       playbooksRepo,
			Subpath:    subpath,
			Reason:     "repo path " + path + " is not readable: " + err.Error(),
		}
	}
	if !info.IsDir() {
		return RepoPathStatus{
			Configured: true,
			Path:       path,
			Repo:       playbooksRepo,
			Subpath:    subpath,
			Reason:     "repo path " + path + " is not a directory",
		}
	}
	if !hasGitInAncestry(path, subpath) {
		return RepoPathStatus{
			Configured: true,
			Path:       path,
			Repo:       playbooksRepo,
			Subpath:    subpath,
			Reason:     "repo path " + path + " has no .git entry in itself or its ancestry — point at the playbooks repo checkout's root (or the playbooks subpath within it)",
		}
	}
	return RepoPathStatus{Configured: true, Valid: true, Path: path, Repo: playbooksRepo, Subpath: subpath}
}

// checkWiki validates that the configured wiki clone is usable. When
// subpath is non-empty, the wiki work-dir lives under a subdir of the
// clone — the `.git` probe walks parents up the configured subpath to
// find a `.git` entry.
func checkWiki(path, repo, subpath string) WikiStatus {
	if path == "" {
		return WikiStatus{
			Reason: "no wiki clone path configured — wiki promote feature disabled. Set paths.wiki_dir in the active profile.",
		}
	}
	info, err := osStat(path)
	if err != nil {
		return WikiStatus{
			Configured: true,
			Path:       path,
			Repo:       repo,
			Subpath:    subpath,
			Reason:     "wiki path " + path + " is not readable: " + err.Error(),
		}
	}
	if !info.IsDir() {
		return WikiStatus{
			Configured: true,
			Path:       path,
			Repo:       repo,
			Subpath:    subpath,
			Reason:     "wiki path " + path + " is not a directory",
		}
	}
	if !hasGitInAncestry(path, subpath) {
		return WikiStatus{
			Configured: true,
			Path:       path,
			Repo:       repo,
			Subpath:    subpath,
			Reason:     "wiki path " + path + " has no .git entry in itself or its ancestry — point at the wiki checkout's root (or the wiki subpath within it)",
		}
	}
	return WikiStatus{Configured: true, Valid: true, Path: path, Repo: repo, Subpath: subpath}
}

// hasGitInAncestry reports whether `<path>/.git` exists, walking up
// `subpath`-many segments. Used by the wiki / sessions capability
// checks: when the work-dir is `<clone>/<subpath>`, the `.git` lives
// at the clone root, not at the work-dir.
func hasGitInAncestry(path, subpath string) bool {
	cur := path
	if _, err := osStat(cur + "/.git"); err == nil {
		return true
	}
	// Pop one segment per separator in subpath. Single-segment subpaths
	// like "wikis" pop once; deeper "wikis/v2" pops twice.
	segments := 0
	for _, r := range subpath {
		if r == '/' {
			segments++
		}
	}
	if subpath != "" {
		segments++
	}
	for i := 0; i < segments; i++ {
		// strip last segment
		idx := -1
		for j := len(cur) - 1; j >= 0; j-- {
			if cur[j] == '/' {
				idx = j
				break
			}
		}
		if idx <= 0 {
			return false
		}
		cur = cur[:idx]
		if _, err := osStat(cur + "/.git"); err == nil {
			return true
		}
	}
	return false
}

// checkSessions validates that the configured sessions clone is usable.
// When subpath is non-empty the work-dir lives under a subdir of the
// clone — the `.git` probe walks parents (hasGitInAncestry).
func checkSessions(path, repo, subpath string) SessionsStatus {
	if path == "" {
		return SessionsStatus{
			Reason: "no sessions clone path configured — sessions upstream feature disabled. Set paths.upstream_sessions_dir in the active profile.",
		}
	}
	info, err := osStat(path)
	if err != nil {
		return SessionsStatus{
			Configured: true,
			Path:       path,
			Repo:       repo,
			Subpath:    subpath,
			Reason:     "sessions path " + path + " is not readable: " + err.Error(),
		}
	}
	if !info.IsDir() {
		return SessionsStatus{
			Configured: true,
			Path:       path,
			Repo:       repo,
			Subpath:    subpath,
			Reason:     "sessions path " + path + " is not a directory",
		}
	}
	if !hasGitInAncestry(path, subpath) {
		return SessionsStatus{
			Configured: true,
			Path:       path,
			Repo:       repo,
			Subpath:    subpath,
			Reason:     "sessions path " + path + " has no .git entry in itself or its ancestry — point at the sessions checkout's root (or the sessions subpath within it)",
		}
	}
	return SessionsStatus{Configured: true, Valid: true, Path: path, Repo: repo, Subpath: subpath}
}

// checkGH runs `gh --version` then `gh auth status`. Both have to be
// callable (binary on PATH, auth token loaded) for PR creation to work.
//
// We don't combine the two — the version probe distinguishes
// "gh missing entirely" from "gh installed but not authed", which makes
// the operator-facing log line actionable.
func checkGH(ctx context.Context) GHStatus {
	if _, err := exec.LookPath("gh"); err != nil {
		return GHStatus{
			Reason: "gh CLI not found on PATH — install it from https://cli.github.com/ to enable playbook → PR push",
		}
	}
	versionCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	versionOut, err := exec.CommandContext(versionCtx, "gh", "--version").Output()
	if err != nil {
		return GHStatus{
			Reason: "found gh on PATH but `gh --version` failed: " + err.Error(),
		}
	}
	version := strings.TrimSpace(strings.SplitN(string(versionOut), "\n", 2)[0])

	authCtx, cancel2 := context.WithTimeout(ctx, 3*time.Second)
	defer cancel2()
	// `gh auth status --hostname github.com` returns non-zero when not
	// authenticated; the human-readable detail is on stderr. We capture
	// both so we can surface a useful Reason.
	cmd := exec.CommandContext(authCtx, "gh", "auth", "status", "--hostname", "github.com")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	authErr := cmd.Run()
	if authErr != nil {
		// Combined output makes the gh hint obvious ("Run gh auth login …").
		combined := strings.TrimSpace(stderr.String() + stdout.String())
		first := combined
		if i := strings.Index(combined, "\n"); i >= 0 {
			first = combined[:i]
		}
		return GHStatus{
			Installed: true,
			Version:   version,
			Reason:    "gh installed but not authenticated: " + first + " — run `gh auth login`",
		}
	}
	user := extractGHUser(stderr.String() + stdout.String())
	return GHStatus{
		Installed:     true,
		Authenticated: true,
		Version:       version,
		User:          user,
	}
}

// extractGHUser tries to pull the "account <name>" line gh emits in its
// auth-status output. Best-effort: if the format shifts we just leave
// User empty; the booleans alone are enough to gate the UI.
func extractGHUser(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		// Recent gh: "✓ Logged in to github.com account aegirhauksson (keyring)"
		if i := strings.Index(line, "account "); i >= 0 {
			rest := line[i+len("account "):]
			if j := strings.Index(rest, " "); j >= 0 {
				return rest[:j]
			}
			return rest
		}
		// Older gh: "Logged in to github.com as <name>"
		if i := strings.Index(line, " as "); i >= 0 {
			rest := line[i+len(" as "):]
			rest = strings.TrimSuffix(rest, ".")
			if j := strings.Index(rest, " "); j >= 0 {
				return rest[:j]
			}
			return rest
		}
	}
	return ""
}

// logCapabilities prints a tight one-line-per-tool startup summary so
// the operator can tell at a glance which optional features are wired.
// Uses the same charmbracelet/log instance the rest of the launcher
// writes through.
func logCapabilities(c Capabilities) {
	switch {
	case c.GH.Authenticated:
		log.Info("gh CLI ready", "version", c.GH.Version, "user", c.GH.User)
	case c.GH.Installed:
		log.Warn("gh CLI installed but not ready", "reason", c.GH.Reason)
	default:
		log.Warn("gh CLI unavailable — playbook → PR push disabled", "reason", c.GH.Reason)
	}
	switch {
	case c.RepoPath.Valid:
		log.Info("playbooks ready", "path", c.RepoPath.Path, "repo", c.RepoPath.Repo)
	case c.RepoPath.Configured:
		// Operator pointed --repo-path / $TRIAGENT_REPO_PATH at
		// something that didn't validate — that's a misconfiguration
		// they should see loudly.
		log.Warn("playbooks path invalid — playbook → PR push disabled", "reason", c.RepoPath.Reason)
	default:
		// No path configured AND auto-detection (CWD probe in
		// checkCapabilities) didn't find a playbooks repo checkout. This
		// is the normal opt-in path for operators who never want
		// playbook PRs from this launcher; INFO keeps it out of the
		// way unless they're looking.
		log.Info("playbook → PR push disabled", "reason", c.RepoPath.Reason)
	}
	if c.Wiki.Configured {
		if c.Wiki.Valid {
			log.Info("wiki ready", "path", c.Wiki.Path, "repo", c.Wiki.Repo)
		} else {
			log.Warn("wiki path invalid — wiki promote disabled", "reason", c.Wiki.Reason)
		}
	}
	if c.Sessions.Configured {
		if c.Sessions.Valid {
			log.Info("sessions ready", "path", c.Sessions.Path, "repo", c.Sessions.Repo)
		} else {
			log.Warn("sessions path invalid — sessions upstream feature disabled", "reason", c.Sessions.Reason)
		}
	}
	switch {
	case c.Claude.Installed:
		log.Info("claude CLI ready", "version", c.Claude.Version)
	default:
		// claude is a hard dependency for sessions, so this is WARN —
		// the launcher boots fine but every "start investigation" will
		// fail at spawn time without it.
		log.Warn("claude CLI unavailable — investigations cannot start", "reason", c.Claude.Reason)
	}
}

// MarshalJSON emits the cached capabilities for the frontend. Kept as a
// thin wrapper so the handler is easy to grep for.
func (c Capabilities) MarshalJSON() ([]byte, error) {
	type alias Capabilities
	return json.Marshal((alias)(c))
}
