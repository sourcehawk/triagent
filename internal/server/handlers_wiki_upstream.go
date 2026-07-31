package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// wikiUpstreamStatus is what GET /api/wiki-upstream returns: enough info
// for the wiki home header to render "synced from <repo> · last synced N
// ago" and to decide whether the Sync button should be enabled.
type wikiUpstreamStatus struct {
	Repo             string `json:"repo"`
	Dir              string `json:"dir"`
	GitCheckout      bool   `json:"gitCheckout"`
	Commit           string `json:"commit,omitempty"`
	LastSynced       string `json:"lastSynced,omitempty"` // RFC3339 of last fetch (mtime of .git/FETCH_HEAD)
	RemoteAhead      int    `json:"remoteAhead"`
	RemoteAheadError string `json:"remoteAheadError,omitempty"`
	Error            string `json:"error,omitempty"`
}

// handleWikiUpstreamStatus reports whether the wiki upstream clone is
// healthy and how recent its content is. Used by the wiki home header to
// render the sync button + an "N ago" hint.
//
// GET /api/wiki-upstream
func (a *apiHandlers) handleWikiUpstreamStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	status := wikiUpstreamStatus{
		Repo: a.opts.WikiRepo,
		Dir:  a.opts.WikiPath,
	}
	if a.opts.WikiPath == "" {
		status.Error = "no wiki path is configured"
		writeJSON(w, http.StatusOK, status)
		return
	}
	gitDir := upstreamGitDir(a.opts.WikiCloneRoot, a.opts.WikiPath)
	if _, err := os.Stat(filepath.Join(gitDir, ".git")); err == nil {
		status.GitCheckout = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		status.Error = err.Error()
		writeJSON(w, http.StatusOK, status)
		return
	}
	if status.GitCheckout {
		// HEAD commit + committer-time. Best-effort: failures populate
		// Error but still return OK so the header can render with what
		// we have.
		if commit, err := runGitCapture(r.Context(), gitDir, "rev-parse", "--short", "HEAD"); err == nil {
			status.Commit = strings.TrimSpace(commit)
		}
		// "Last synced" = when we last reconciled with the remote.
		// git rewrites .git/FETCH_HEAD on every fetch even if origin
		// hasn't moved, so its mtime is the right source. HEAD's
		// committer-date would otherwise show "42h ago" right after a
		// successful sync of an unchanged upstream.
		if t, ok := gitLastSyncTime(r.Context(), gitDir); ok {
			status.LastSynced = t.UTC().Format(time.RFC3339)
		}
		// Remote-ahead probe: quick fetch + rev-list. Bounded so a hung
		// remote doesn't block the page load.
		fetchCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if _, err := runGitCapture(fetchCtx, gitDir, "fetch", "--quiet", "origin"); err != nil {
			status.RemoteAheadError = err.Error()
		} else if out, err := runGitCapture(fetchCtx, gitDir, "rev-list", "--count", "HEAD..origin/HEAD"); err != nil {
			// Some remotes don't expose origin/HEAD as a symbolic ref;
			// fall back to origin/main. Still best-effort.
			if out2, err2 := runGitCapture(fetchCtx, gitDir, "rev-list", "--count", "HEAD..origin/main"); err2 == nil {
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

// handleWikiUpstreamSync runs `git fetch origin && git reset --hard
// origin/HEAD` against the wiki clone dir.
//
// Unlike the playbook sync handler this does NOT call loadMeta() after the
// sync — the wiki home-page handlers re-read the vault on each request, so a
// fresh sync is reflected on the next API call without any in-memory cache
// invalidation.
//
// POST /api/wiki-upstream/sync
func (a *apiHandlers) handleWikiUpstreamSync(w http.ResponseWriter, r *http.Request) {
	if !a.requireUpstreamRepo(w, "wiki", a.opts.WikiRepo) {
		return
	}
	if a.opts.WikiPath == "" {
		writeError(w, http.StatusServiceUnavailable, "no wiki path configured")
		return
	}
	gitDir := upstreamGitDir(a.opts.WikiCloneRoot, a.opts.WikiPath)
	if _, err := os.Stat(filepath.Join(gitDir, ".git")); err != nil {
		writeError(w, http.StatusFailedDependency, fmt.Sprintf("wiki dir %s is not a git checkout — sync requires a cloned repo", missingCheckoutDetail(a.opts.WikiCloneRoot, a.opts.WikiPath)))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if out, err := runGitCapture(ctx, gitDir, "fetch", "origin"); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("git fetch: %v\n%s", err, out))
		return
	}
	if out, err := runGitCapture(ctx, gitDir, "reset", "--hard", "origin/HEAD"); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("git reset: %v\n%s", err, out))
		return
	}
	commit, _ := runGitCapture(ctx, gitDir, "rev-parse", "--short", "HEAD")
	lastSynced := time.Now().UTC().Format(time.RFC3339)
	if t, ok := gitLastSyncTime(ctx, gitDir); ok {
		lastSynced = t.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"commit":     strings.TrimSpace(commit),
		"lastSynced": lastSynced,
	})
}

// gitLastSyncTime returns when origin was last fetched into the given
// git checkout. It uses .git/FETCH_HEAD's mtime — git rewrites that
// file on every fetch, even when origin's tip is unchanged, so it's a
// faithful "we talked to origin at this moment" stamp. Resolves the
// gitdir via `git rev-parse` so worktrees / submodules (where .git is
// a file pointing elsewhere) are handled correctly.
func gitLastSyncTime(ctx context.Context, repoPath string) (time.Time, bool) {
	gitDirRaw, err := runGitCapture(ctx, repoPath, "rev-parse", "--git-dir")
	if err != nil {
		return time.Time{}, false
	}
	gitDir := strings.TrimSpace(gitDirRaw)
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(repoPath, gitDir)
	}
	info, err := os.Stat(filepath.Join(gitDir, "FETCH_HEAD"))
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}
