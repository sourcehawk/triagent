package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// upstreamStatus is what GET /api/playbooks-upstream returns: enough
// info for the playbooks tab header to render "synced from <repo> ·
// last synced N ago" and to decide whether the Sync button should be
// disabled (e.g. clone is missing).
//
// RemoteAhead is the count of upstream commits not yet applied
// locally. We compute it by running a quick `git fetch origin` (5s
// timeout) followed by `rev-list HEAD..origin/HEAD --count`. Failures
// surface as RemoteAheadError without blocking the rest of the
// response — the operator can still see "you're at <commit>", just
// without the "N changes available" hint.
type upstreamStatus struct {
	Repo              string `json:"repo"`
	Dir               string `json:"dir"`
	GitCheckout       bool   `json:"gitCheckout"`
	Commit            string `json:"commit,omitempty"`
	LastSynced        string `json:"lastSynced,omitempty"` // RFC3339 of last fetch (mtime of .git/FETCH_HEAD)
	RemoteAhead       int    `json:"remoteAhead"`
	RemoteAheadError  string `json:"remoteAheadError,omitempty"`
	Error             string `json:"error,omitempty"`
}

// handlePlaybooksUpstreamStatus reports whether the upstream clone is
// healthy and how recent its content is. Used by the playbooks tab
// header to render the sync button + an "N ago" hint.
//
// GET /api/playbooks-upstream
func (a *apiHandlers) handlePlaybooksUpstreamStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	status := upstreamStatus{
		Repo: a.opts.PlaybooksRepo,
		Dir:  a.opts.PluginPlaybooksDir,
	}
	if a.opts.PluginPlaybooksDir == "" {
		status.Error = "no system playbooks dir is configured"
		writeJSON(w, http.StatusOK, status)
		return
	}
	if _, err := os.Stat(filepath.Join(a.opts.PluginPlaybooksDir, ".git")); err == nil {
		status.GitCheckout = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		status.Error = err.Error()
		writeJSON(w, http.StatusOK, status)
		return
	}
	if status.GitCheckout {
		// HEAD commit + author-time. Best-effort: failures populate
		// Error but still return OK so the tab can render with what
		// we have.
		if commit, err := runGitCapture(r.Context(), a.opts.PluginPlaybooksDir, "rev-parse", "--short", "HEAD"); err == nil {
			status.Commit = strings.TrimSpace(commit)
		}
		// "Last synced" = mtime of .git/FETCH_HEAD; HEAD's committer-date
		// would freeze when origin doesn't advance, making the sync button
		// look like a no-op. See gitLastSyncTime in handlers_wiki_upstream.go.
		if t, ok := gitLastSyncTime(r.Context(), a.opts.PluginPlaybooksDir); ok {
			status.LastSynced = t.UTC().Format(time.RFC3339)
		}
		// remote-ahead probe: quick fetch + rev-list. Bounded so a
		// hung remote doesn't block the page load.
		fetchCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if _, err := runGitCapture(fetchCtx, a.opts.PluginPlaybooksDir, "fetch", "--quiet", "origin"); err != nil {
			status.RemoteAheadError = err.Error()
		} else if out, err := runGitCapture(fetchCtx, a.opts.PluginPlaybooksDir, "rev-list", "--count", "HEAD..origin/HEAD"); err != nil {
			// Some remotes don't expose origin/HEAD as a symbolic
			// ref; fall back to origin/main. Still best-effort.
			if out2, err2 := runGitCapture(fetchCtx, a.opts.PluginPlaybooksDir, "rev-list", "--count", "HEAD..origin/main"); err2 == nil {
				status.RemoteAhead = atoiOrZero(strings.TrimSpace(out2))
			} else {
				status.RemoteAheadError = err.Error()
			}
			_ = out
		} else {
			status.RemoteAhead = atoiOrZero(strings.TrimSpace(out))
		}
	}
	writeJSON(w, http.StatusOK, status)
}

func atoiOrZero(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// handlePlaybooksUpstreamSync runs `git fetch origin && git reset
// --hard origin/HEAD` against the clone dir. We use reset --hard
// rather than pull --rebase because:
//
//   - The clone is a read-only mirror from the operator's perspective.
//     They author via the editor's user dir layered on top, and push
//     via the PR flow which creates dedicated branches + commits in a
//     clean working tree (the push flow refuses dirty state). So a
//     hard reset can't lose operator work that lives in this dir.
//   - It guarantees the clone exactly matches origin/HEAD, no merge
//     state to reason about.
//
// Returns the new commit hash + author-time on success so the
// frontend can update its "last synced" hint without a second fetch.
//
// POST /api/playbooks-upstream/sync
func (a *apiHandlers) handlePlaybooksUpstreamSync(w http.ResponseWriter, r *http.Request) {
	if !a.requireUpstreamRepo(w, "playbooks", a.opts.PlaybooksRepo) {
		return
	}
	if a.opts.PluginPlaybooksDir == "" {
		writeError(w, http.StatusServiceUnavailable, "no system playbooks dir configured")
		return
	}
	if _, err := os.Stat(filepath.Join(a.opts.PluginPlaybooksDir, ".git")); err != nil {
		writeError(w, http.StatusFailedDependency, fmt.Sprintf("playbooks dir %s is not a git checkout — sync requires a cloned repo", a.opts.PluginPlaybooksDir))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if out, err := runGitCapture(ctx, a.opts.PluginPlaybooksDir, "fetch", "origin"); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("git fetch: %v\n%s", err, out))
		return
	}
	if out, err := runGitCapture(ctx, a.opts.PluginPlaybooksDir, "reset", "--hard", "origin/HEAD"); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("git reset: %v\n%s", err, out))
		return
	}
	commit, _ := runGitCapture(ctx, a.opts.PluginPlaybooksDir, "rev-parse", "--short", "HEAD")
	lastSynced := time.Now().UTC().Format(time.RFC3339)
	if t, ok := gitLastSyncTime(ctx, a.opts.PluginPlaybooksDir); ok {
		lastSynced = t.UTC().Format(time.RFC3339)
	}
	// Refresh the metaCache so the playbooks tab reflects the new
	// upstream content immediately rather than after a launcher
	// restart.
	if meta, err := loadMeta(ctx, a.opts.PluginPlaybooksDir, a.opts.SystemPlaybooksDir); err == nil {
		a.metaCache.set(meta)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"commit":     strings.TrimSpace(commit),
		"lastSynced": lastSynced,
	})
}

// runGitCapture runs git in the given dir and returns combined output.
// Defined here (instead of reusing repo_pr.go's runGit) because the
// sync endpoint cares about both stdout + stderr in one go and
// repo_pr's helper is shaped for the push flow.
func runGitCapture(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return buf.String(), err
	}
	return buf.String(), nil
}
