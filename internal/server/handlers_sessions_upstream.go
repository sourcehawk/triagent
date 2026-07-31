package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// sessionsUpstreamStatus mirrors wikiUpstreamStatus but for the sessions
// repo. Drives the SessionUpstreamHeader on /.
type sessionsUpstreamStatus struct {
	Repo             string `json:"repo"`
	Dir              string `json:"dir"`
	GitCheckout      bool   `json:"gitCheckout"`
	Commit           string `json:"commit,omitempty"`
	LastSynced       string `json:"lastSynced,omitempty"`
	RemoteAhead      int    `json:"remoteAhead"`
	RemoteAheadError string `json:"remoteAheadError,omitempty"`
	Error            string `json:"error,omitempty"`
}

// handleSessionsUpstreamStatus reports whether the sessions upstream clone is
// healthy and how recent its content is.
//
// GET /api/sessions-upstream
func (a *apiHandlers) handleSessionsUpstreamStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	repo := SessionsRepoFor(a.prof, a.opts.SessionsRepo)
	status := sessionsUpstreamStatus{Repo: repo, Dir: a.opts.SessionsPath}
	if a.opts.SessionsPath == "" {
		status.Error = "no sessions path is configured"
		writeJSON(w, http.StatusOK, status)
		return
	}
	gitDir := upstreamGitDir(a.opts.SessionsCloneRoot, a.opts.SessionsPath)
	if _, err := os.Stat(filepath.Join(gitDir, ".git")); err == nil {
		status.GitCheckout = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		status.Error = err.Error()
		writeJSON(w, http.StatusOK, status)
		return
	}
	if status.GitCheckout {
		if commit, err := runGitCapture(r.Context(), gitDir, "rev-parse", "--short", "HEAD"); err == nil {
			status.Commit = strings.TrimSpace(commit)
		}
		// "Last synced" = mtime of .git/FETCH_HEAD; HEAD's committer-date
		// would freeze when origin doesn't advance, making the sync button
		// look like a no-op. See gitLastSyncTime in handlers_wiki_upstream.go.
		if t, ok := gitLastSyncTime(r.Context(), gitDir); ok {
			status.LastSynced = t.UTC().Format(time.RFC3339)
		}
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

// handleSessionsUpstreamSync runs `git fetch origin && git reset --hard
// origin/HEAD` against the sessions clone dir.
//
// POST /api/sessions-upstream/refresh-pr-states
//
// Re-pulls `gh pr list` for the upstream sessions repo and reconciles
// each known PushURL against the result. Returns the number of
// investigations whose PR state changed. The frontend hits this on
// /investigations page mount + after a sync click so the "PR open" /
// "merged" / "closed" pills stay fresh without the operator having to
// restart the launcher.
func (a *apiHandlers) handleSessionsUpstreamRefreshPRStates(w http.ResponseWriter, r *http.Request) {
	repo := SessionsRepoFor(a.prof, a.opts.SessionsRepo)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	changed, err := a.manager.RefreshPRStates(ctx, repo)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("refresh PR states: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "changed": changed})
}

// POST /api/sessions-upstream/sync
func (a *apiHandlers) handleSessionsUpstreamSync(w http.ResponseWriter, r *http.Request) {
	if !a.requireUpstreamRepo(w, "sessions", a.opts.SessionsRepo) {
		return
	}
	if a.opts.SessionsPath == "" {
		writeError(w, http.StatusServiceUnavailable, "no sessions path configured")
		return
	}
	// Git operations run against the clone root (where `.git` lives).
	// SessionsPath is the work-dir under the clone — fine for reading
	// per-session files, but `git fetch && reset` needs the root.
	cloneRoot := upstreamGitDir(a.opts.SessionsCloneRoot, a.opts.SessionsPath)
	if _, err := os.Stat(filepath.Join(cloneRoot, ".git")); err != nil {
		writeError(w, http.StatusFailedDependency, fmt.Sprintf("sessions dir %s is not a git checkout", cloneRoot))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if out, err := runGitCapture(ctx, cloneRoot, "fetch", "origin"); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("git fetch: %v\n%s", err, out))
		return
	}
	if out, err := runGitCapture(ctx, cloneRoot, "reset", "--hard", "origin/HEAD"); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("git reset: %v\n%s", err, out))
		return
	}
	commit, _ := runGitCapture(ctx, cloneRoot, "rev-parse", "--short", "HEAD")
	lastSynced := time.Now().UTC().Format(time.RFC3339)
	if t, ok := gitLastSyncTime(ctx, cloneRoot); ok {
		lastSynced = t.UTC().Format(time.RFC3339)
	}
	// Reconcile any local Investigations whose slug now matches an
	// upstream session.md — peer-pushed sessions we just synced down
	// must flip Pushed=true immediately, not on next launcher restart.
	// Without this, the sidebar checkmark lags real upstream state by
	// however long the operator leaves the launcher running.
	if a.manager != nil {
		a.manager.ReconcileUpstreamPushed(a.opts.SessionsPath)
	}
	// Fold a PR-state refresh into the same sync click so a freshly
	// merged/closed PR doesn't need a separate trigger. Best-effort —
	// failures are routine and we don't want to fail the sync over
	// gh being unhappy.
	repo := SessionsRepoFor(a.prof, a.opts.SessionsRepo)
	if a.manager != nil {
		_, _ = a.manager.RefreshPRStates(ctx, repo)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"commit":     strings.TrimSpace(commit),
		"lastSynced": lastSynced,
	})
}

// sessionCard is the per-session row returned by the list handler. Body is
// fetched per-click via handleSessionsUpstreamGet.
type sessionCard struct {
	Slug            string         `json:"slug"`
	Title           string         `json:"title"`
	Date            string         `json:"date"`
	Namespace       string         `json:"namespace"`
	ClustersTouched []string       `json:"clusters_touched,omitempty"`
	Author          sessionAuthor  `json:"author"`
	Sources         sessionSources `json:"sources"`
	// SyncState is the resolver's answer for this slug — synced /
	// closed when there's a matching local Investigation, upstream-only
	// otherwise. Lets the UpstreamHome cards render their PR pill
	// without rebuilding the prBySlug map on the frontend.
	SyncState SyncState `json:"syncState"`
}

// GET /api/sessions-upstream/sessions
func (a *apiHandlers) handleSessionsUpstreamList(w http.ResponseWriter, r *http.Request) {
	if a.opts.SessionsPath == "" {
		writeJSON(w, http.StatusOK, map[string]any{"sessions": []sessionCard{}})
		return
	}
	// SessionsPath is the work-dir under the upstream-sessions clone
	// (cloneRoot + sessions_path on the default profile). Per-month/
	// per-slug session dirs land directly under it, mirroring the
	// historical `sessions/` layout that the default profile preserves
	// via sessions_path=sessions.
	cards, err := scanSessionsRepo(a.opts.SessionsPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Build a slug → InvestigationDTO index once so we can decorate
	// cards with their SyncState (and apply the closed-PR filter)
	// without N×List walks. The scan is local-only — no per-card I/O.
	// Manager is nil in some unit tests; treat that as "no local
	// investigations" rather than panicking.
	bySlug := map[string]InvestigationDTO{}
	if a.manager != nil {
		for _, dto := range a.manager.List() {
			// Defensive: computeSessionSlug is non-empty for every
			// well-formed Investigation today, but a future change
			// (e.g. an Investigation with an empty Namespace before
			// preflight finishes) shouldn't silently key empty
			// string into the join and collide with itself.
			if dto.Slug != "" {
				bySlug[dto.Slug] = dto
			}
		}
	}
	// Hide sessions whose corresponding local investigation has its
	// PR closed-without-merge: the session.md may still be on disk
	// (the local clone was on the side branch when we pushed) but
	// it'll never land on main, so showing it as "available upstream"
	// is misleading. Open and merged PRs stay visible. Sessions
	// without a matching local investigation pass through unchanged.
	out := cards[:0]
	for _, c := range cards {
		dto, hasLocal := bySlug[c.Slug]
		if hasLocal && dto.PRState == PRStateClosed {
			continue
		}
		c.SyncState = sessionSyncStateFor(sessionSyncStateInputs{
			HasLocal:   hasLocal,
			Pushed:     dto.Pushed,
			PushURL:    dto.PushURL,
			PRState:    dto.PRState,
			PRMergedAt: dto.PRMergedAt,
			PRClosedAt: dto.PRClosedAt,
		})
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// GET /api/sessions-upstream/sessions/{slug}
func (a *apiHandlers) handleSessionsUpstreamGet(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !sessionSlugPattern.MatchString(slug) {
		writeError(w, http.StatusBadRequest, "invalid slug")
		return
	}
	if a.opts.SessionsPath == "" {
		writeError(w, http.StatusServiceUnavailable, "no sessions path configured")
		return
	}
	p := filepath.Join(a.opts.SessionsPath, monthDirForSlug(slug), slug, "session.md")
	raw, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	fmBytes, body, err := wikiSplitFrontmatter(raw)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "frontmatter: "+err.Error())
		return
	}
	var fm sessionFrontmatter
	if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
		writeError(w, http.StatusInternalServerError, "frontmatter parse: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"slug":        slug,
		"frontmatter": fm,
		"body":        string(body),
	})
}

// GET /api/sessions-upstream/sessions/{slug}/bundle
func (a *apiHandlers) handleSessionsUpstreamBundle(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !sessionSlugPattern.MatchString(slug) {
		writeError(w, http.StatusBadRequest, "invalid slug")
		return
	}
	if a.opts.SessionsPath == "" {
		writeError(w, http.StatusServiceUnavailable, "no sessions path configured")
		return
	}
	// Sessions written by triagent ≥ c336bba use session.triagent.json. Sessions
	// pushed by the legacy c1-plugins binary (and never re-pushed since)
	// still have session.c1inv.json on disk, with frontmatter pointing at
	// the same legacy name. Try the current filename first, fall back to
	// the legacy one so "replay full transcript" works on historical PRs
	// without rewriting upstream repo history.
	sessionDir := filepath.Join(a.opts.SessionsPath, monthDirForSlug(slug), slug)
	var f *os.File
	var openErr error
	for _, name := range []string{"session.triagent.json", "session.c1inv.json"} {
		f, openErr = os.Open(filepath.Join(sessionDir, name))
		if openErr == nil {
			break
		}
		if !errors.Is(openErr, fs.ErrNotExist) {
			writeError(w, http.StatusInternalServerError, openErr.Error())
			return
		}
	}
	if openErr != nil {
		writeError(w, http.StatusNotFound, "bundle not found")
		return
	}
	defer func() { _ = f.Close() }()
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.Copy(w, f)
}

// scanSessionsRepo walks sessions/<YYYY-MM>/<slug>/session.md and parses
// frontmatter. Skips directories that don't match the expected layout
// (forward-compat: an attachments/ folder later won't break the listing).
//
// Always returns a non-nil slice so JSON marshalling produces `[]` rather
// than `null` for an empty repo — the frontend's `items` state treats
// null as "still loading", and a freshly-cloned empty repo (no
// sessions/ subdir yet) would otherwise pin the loading spinner forever.
func scanSessionsRepo(root string) ([]sessionCard, error) {
	out := []sessionCard{}
	months, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	for _, m := range months {
		if !m.IsDir() {
			continue
		}
		slugs, err := os.ReadDir(filepath.Join(root, m.Name()))
		if err != nil {
			continue
		}
		for _, s := range slugs {
			if !s.IsDir() || !sessionSlugPattern.MatchString(s.Name()) {
				continue
			}
			p := filepath.Join(root, m.Name(), s.Name(), "session.md")
			raw, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			fmBytes, _, err := wikiSplitFrontmatter(raw)
			if err != nil {
				continue
			}
			var fm sessionFrontmatter
			if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
				continue
			}
			out = append(out, sessionCard{
				Slug:            s.Name(),
				Title:           fm.Title,
				Date:            fm.Date,
				Namespace:       fm.Namespace,
				ClustersTouched: fm.ClustersTouched,
				Author:          fm.Author,
				Sources:         fm.Sources,
			})
		}
	}
	// newest first
	sort.Slice(out, func(i, j int) bool { return out[i].Date > out[j].Date })
	return out, nil
}
