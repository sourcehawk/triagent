package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sourcehawk/triagent/internal/profile"
)

// pushPlaybookPRRequest is what the frontend POSTs to push a playbook to
// the playbooks repo as a PR. All fields are optional except `yaml`,
// which carries the editor's current content. The launcher applies sane
// defaults for branch/title/body when the operator leaves them blank.
type pushPlaybookPRRequest struct {
	YAML string `json:"yaml"`
	// Type is the upstream type subdir the file lands under
	// (investigation/, general/, etc.). Required: the upstream repo
	// uses directory-as-type, so we can't infer it from the YAML.
	Type   string `json:"type"`
	Branch string `json:"branch,omitempty"`
	Title  string `json:"title,omitempty"`
	Body   string `json:"body,omitempty"`
	Base   string `json:"base,omitempty"` // target branch on origin; defaults to "main"
}

// pushPlaybookPRResult is the success payload — just the PR URL gh
// printed plus the branch we pushed to (handy for ops who want to
// `git checkout` the branch after the fact).
type pushPlaybookPRResult struct {
	URL    string `json:"url"`
	Branch string `json:"branch"`
	Base   string `json:"base"`
}

// PlaybooksRepoFor returns the playbooks repo (OWNER/REPO) to use,
// preferring a per-call override over the profile's default. Both the
// clone URL and the PR upstream derive from the returned value. The
// override argument is preserved as a seam for tests and for any
// future per-session override; the launcher always passes "".
func PlaybooksRepoFor(prof *profile.Profile, override string) string {
	if override != "" {
		return override
	}
	if prof == nil {
		return ""
	}
	return prof.Defaults.PlaybooksRepo
}

// WikiRepoFor returns the wiki repo (OWNER/REPO) to use, preferring
// a per-call override over the profile's default.
func WikiRepoFor(prof *profile.Profile, override string) string {
	if override != "" {
		return override
	}
	if prof == nil {
		return ""
	}
	return prof.Defaults.WikiRepo
}

// SessionsRepoFor returns the sessions repo (OWNER/REPO) to use,
// preferring a per-call override over the profile's default.
func SessionsRepoFor(prof *profile.Profile, override string) string {
	if override != "" {
		return override
	}
	if prof == nil {
		return ""
	}
	return prof.Defaults.SessionsRepo
}

// PlaybooksCloneURL turns the OWNER/REPO into a clone URL the
// launcher hands to `git clone`. We use HTTPS rather than SSH so
// the operator doesn't need a configured SSH agent for the
// read-only clone path; pushes go through gh/git which handle auth
// independently.
func PlaybooksCloneURL(repo string) string {
	return "https://github.com/" + repo + ".git"
}

// branchSafe restricts what we accept as a branch name. Allows enough
// for "playbook/<id>-<date>"-shaped names without opening the door to
// argv injection or accidental refspec characters.
var branchSafe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,99}$`)

// pushPlaybookPR is the worker behind POST /api/playbooks/{id}/push-pr.
// Sequence:
//  1. Sanity-check capabilities + inputs.
//  2. Recover from any dirty state via resetTreeIfDirty (the clone is
//     launcher-managed, so reset is safe).
//  3. Fetch + branch from the latest base.
//  4. Write the YAML, validate, commit, push.
//  5. Open the PR via gh.
//
// Each step has bounded timeouts; subprocess output is captured into the
// returned error so the launcher's HTTP response carries actionable
// detail instead of a generic "git failed".
//
// cloneRoot is the cwd for every git invocation — the path `git clone`
// wrote into. workDir is where the `<type>/<id>.yaml` tree lives;
// equals cloneRoot on flat layouts, or `cloneRoot/<subpath>` when the
// profile sets defaults.playbooks_path. Running git from cloneRoot
// lets `git checkout -B branch origin/<base>` survive even when the
// subpath dir doesn't exist on the target ref.
func pushPlaybookPR(
	ctx context.Context,
	caps Capabilities,
	cloneRoot string,
	workDir string,
	upstreamRepo string,
	mcpBin string,
	id string,
	req pushPlaybookPRRequest,
) (*pushPlaybookPRResult, []string, error) {
	// Empty upstream falls back to the default baseline; lets callers
	// pass through Options blindly without a nil-check ladder.
	if upstreamRepo == "" {
		upstreamRepo = "sourcehawk/triagent-playbooks"
	}
	if !caps.GH.Authenticated {
		return nil, nil, fmt.Errorf("gh CLI not ready: %s", caps.GH.Reason)
	}
	if !caps.RepoPath.Valid {
		return nil, nil, fmt.Errorf("repo path not ready: %s", caps.RepoPath.Reason)
	}
	if !playbookIDPattern.MatchString(id) {
		return nil, nil, fmt.Errorf("invalid playbook id %q", id)
	}
	pbType := strings.TrimSpace(req.Type)
	if pbType == "" {
		return nil, nil, fmt.Errorf("type is required (the type slot the file lives under in upstream — investigation, general, …)")
	}
	if !typePattern.MatchString(pbType) {
		return nil, nil, fmt.Errorf("invalid type %q (allowed: alphanumeric, _-, up to 64 chars)", pbType)
	}
	body := strings.TrimSpace(req.YAML)
	if body == "" {
		return nil, nil, fmt.Errorf("yaml is required")
	}
	bodyID, err := parseYAMLPlaybookID(body)
	if err != nil {
		return nil, []string{err.Error()}, nil
	}
	if bodyID != id {
		return nil, []string{fmt.Sprintf("YAML id %q does not match URL id %q", bodyID, id)}, nil
	}
	if errs, err := validatePlaybookViaSubprocess(ctx, mcpBin, body); err != nil {
		return nil, nil, fmt.Errorf("invoke validator: %w", err)
	} else if len(errs) > 0 {
		return nil, errs, nil
	}

	base := strings.TrimSpace(req.Base)
	if base == "" {
		base = "main"
	}
	branch := strings.TrimSpace(req.Branch)
	if branch == "" {
		branch = fmt.Sprintf("playbook/%s-%s", id, time.Now().UTC().Format("20060102-150405"))
	}
	if !branchSafe.MatchString(branch) {
		return nil, nil, fmt.Errorf("branch name %q has unsupported characters (allowed: [A-Za-z0-9._/-])", branch)
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = fmt.Sprintf("playbook(%s): update", id)
	}
	prBody := strings.TrimSpace(req.Body)
	if prBody == "" {
		prBody = fmt.Sprintf("Updates the `%s` investigation playbook via the triagent editor.", id)
	}

	// Recover from a dirty tree before checkout. The upstream-playbooks
	// clone is launcher-managed — operators author via the editor's user
	// dir layered on top, never via this checkout (see
	// handlePlaybooksUpstreamSync's comment). Common causes of dirt are
	// the local type-delete handler (which os.RemoveAll's a tracked dir)
	// or a prior PR flow that exited between write and commit. None of
	// those are operator work; reset --hard HEAD restores HEAD without
	// touching commits, then the checkout below switches to the new
	// branch from origin/<base>.
	if err := resetTreeIfDirty(ctx, cloneRoot); err != nil {
		return nil, nil, err
	}

	// Fetch upstream so the new branch is anchored to the latest base.
	if out, err := runGit(ctx, cloneRoot, "fetch", "origin", base); err != nil {
		return nil, nil, fmt.Errorf("git fetch origin %s: %w (%s)", base, err, out)
	}
	// Create the new branch from origin/<base>. -B = create or reset; we
	// only let you reach this point on a clean tree, so reset is safe.
	if out, err := runGit(ctx, cloneRoot, "checkout", "-B", branch, "origin/"+base); err != nil {
		return nil, nil, fmt.Errorf("git checkout -B %s origin/%s: %w (%s)", branch, base, err, out)
	}

	// Write the YAML at `<workDir>/<pbType>/<id>.yaml`. relToClone is
	// the path relative to the clone root so we can `git add` it from
	// cwd=cloneRoot, and includes any configured subpath. The
	// version-strip below handles any leftover `version:` field
	// operators may have hand-edited in.
	target := filepath.Join(workDir, pbType, id+".yaml")
	relToClone, err := filepath.Rel(cloneRoot, target)
	if err != nil {
		return nil, nil, fmt.Errorf("compute path relative to clone root: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(target), err)
	}
	stripped, err := stripVersionFromPlaybookYAML(body)
	if err != nil {
		return nil, []string{fmt.Sprintf("yaml parse: %v", err)}, nil
	}
	if err := os.WriteFile(target, []byte(stripped), 0o644); err != nil {
		return nil, nil, fmt.Errorf("write %s: %w", target, err)
	}

	if out, err := runGit(ctx, cloneRoot, "add", relToClone); err != nil {
		return nil, nil, fmt.Errorf("git add: %w (%s)", err, out)
	}
	// `git diff --staged --quiet` returns 1 when there are staged changes.
	if changed, err := gitHasStagedChanges(ctx, cloneRoot); err != nil {
		return nil, nil, err
	} else if !changed {
		return nil, nil, fmt.Errorf("no changes to commit — the playbook file already matches what's in the repo")
	}
	if out, err := runGit(ctx, cloneRoot, "commit", "-m", title); err != nil {
		return nil, nil, fmt.Errorf("git commit: %w (%s)", err, out)
	}

	// Push with --force-with-lease so a re-push of the same branch
	// (e.g. operator iterating on a draft PR) updates the existing
	// remote ref instead of bouncing with "fetch first". The fetch
	// before the push gives --force-with-lease a baseline to check
	// against; without it, lease enforcement degrades to plain --force
	// which would silently clobber an unexpected third-party update.
	// Errors from the fetch are ignored: a fresh branch isn't on the
	// remote yet, which is the common case.
	_, _ = runGit(ctx, cloneRoot, "fetch", "origin", branch)
	if out, err := runGit(ctx, cloneRoot, "push", "--force-with-lease", "-u", "origin", branch); err != nil {
		return nil, nil, fmt.Errorf("git push: %w (%s)", err, out)
	}

	url, err := openGHPullRequest(ctx, cloneRoot, upstreamRepo, base, branch, title, prBody)
	if err != nil {
		return nil, nil, err
	}
	return &pushPlaybookPRResult{URL: url, Branch: branch, Base: base}, nil, nil
}

// resetTreeIfDirty clears uncommitted state from the launcher-managed
// upstream-playbooks clone before a PR-style flow runs `git checkout
// -B branch origin/base`. Without this, leftover dirt from a prior
// flow (interrupted PR push, or a local type-delete that removed a
// tracked file out of band) makes the checkout fail.
//
// Hard reset is safe in this clone specifically because operators
// don't author here — same rationale that lets handlePlaybooksUpstreamSync
// fetch + reset --hard origin/HEAD without confirmation.
func resetTreeIfDirty(ctx context.Context, repoPath string) error {
	dirty, err := gitWorkingTreeDirty(ctx, repoPath)
	if err != nil {
		return err
	}
	if !dirty {
		return nil
	}
	if out, err := runGit(ctx, repoPath, "reset", "--hard", "HEAD"); err != nil {
		return fmt.Errorf("git reset --hard HEAD: %w (%s)", err, out)
	}
	return nil
}

// gitWorkingTreeDirty reports whether the tracked working tree has
// uncommitted modifications. PR-style flows call resetTreeIfDirty
// to recover; the raw signal is also exposed for callers that just
// want to know.
//
// `--untracked-files=no` excludes untracked files and dirs from the
// dirty signal: a `git checkout -B` to the upstream branch leaves
// untracked paths in place, and our commit flow only stages a single
// YAML path by name — untracked work alongside it is safe and
// shouldn't block a PR push (operators commonly keep
// `investigation-wip/` style scratch dirs in this clone).
func gitWorkingTreeDirty(ctx context.Context, repoPath string) (bool, error) {
	out, err := runGit(ctx, repoPath, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return false, fmt.Errorf("git status --porcelain: %w (%s)", err, out)
	}
	return strings.TrimSpace(out) != "", nil
}

// gitHasStagedChanges returns true when there's at least one staged
// change. We use this to bail with a friendly "nothing to commit"
// message rather than letting `git commit` print "nothing to commit,
// working tree clean" out-of-band.
func gitHasStagedChanges(ctx context.Context, repoPath string) (bool, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "git", "diff", "--staged", "--quiet")
	cmd.Dir = repoPath
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if exitErr.ExitCode() == 1 {
				return true, nil
			}
		}
		return false, fmt.Errorf("git diff --staged --quiet: %w", err)
	}
	return false, nil
}

// runGit runs git in repoPath with a bounded timeout and returns the
// combined stdout+stderr output for use in error messages.
func runGit(ctx context.Context, repoPath string, args ...string) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "git", args...)
	cmd.Dir = repoPath
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return strings.TrimSpace(buf.String()), err
}

// openGHPullRequest invokes `gh pr create` and parses the URL it prints.
// gh's success output is the PR URL on its own line. Errors capture the
// full output so the operator can read the human-readable detail.
//
// `--repo upstreamRepo` pins the destination to the configured upstream
// regardless of which remote the operator's local checkout points at.
// For a fork-based local setup, gh detects the fork and configures the
// PR head as `<fork-owner>:<branch>` automatically.
func openGHPullRequest(ctx context.Context, repoPath, upstreamRepo, base, branch, title, body string) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "gh", "pr", "create",
		"--repo", upstreamRepo,
		"--base", base,
		"--head", branch,
		"--title", title,
		"--body", body,
	)
	cmd.Dir = repoPath
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gh pr create: %w (%s)", err, strings.TrimSpace(buf.String()))
	}
	out := strings.TrimSpace(buf.String())
	// gh prints multiple lines; the URL is usually the last non-empty
	// one. Walk in reverse to find it.
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "https://") {
			return line, nil
		}
	}
	return "", fmt.Errorf("gh pr create succeeded but no URL found in output: %s", out)
}
