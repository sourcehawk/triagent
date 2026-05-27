package server

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
)

// handleGetPlaybookCommit returns the YAML body of <id> at a specific
// commit. The commit can live in either the user dir's repo or the
// upstream-clone's repo — we try local first, then upstream.
//
// GET /api/playbooks/{id}/commits/{sha}
func (a *apiHandlers) handleGetPlaybookCommit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := r.PathValue("id")
	sha := r.PathValue("sha")
	if id == "" || sha == "" {
		writeError(w, http.StatusBadRequest, "id and sha are required")
		return
	}
	typeName, err := a.resolveTypeFor(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	relPath := filepath.Join(typeName, id+".yaml")
	if a.opts.UserPlaybooksDir != "" {
		if body, err := gitShowAtCommit(r.Context(), a.opts.UserPlaybooksDir, sha, relPath); err == nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"id":     id,
				"sha":    sha,
				"yaml":   body,
				"source": "local",
			})
			return
		}
	}
	if a.opts.PluginPlaybooksDir != "" {
		if body, err := gitShowAtCommit(r.Context(), a.opts.PluginPlaybooksDir, sha, relPath); err == nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"id":     id,
				"sha":    sha,
				"yaml":   body,
				"source": "upstream",
			})
			return
		}
	}
	writeError(w, http.StatusNotFound, "commit not found in either repo")
}

// handleListPlaybookCommits paginates commits touching the playbook
// across both user-dir and upstream-clone repos. Newest first across
// both sources. Capped at limit (default 50).
//
// GET /api/playbooks/{id}/commits?before=<sha>&limit=<n>
func (a *apiHandlers) handleListPlaybookCommits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id required")
		return
	}
	typeName, err := a.resolveTypeFor(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	before := r.URL.Query().Get("before")
	relPath := filepath.Join(typeName, id+".yaml")
	// Errors from gitLogForPlaybook are suppressed: failures here are
	// usually "before~1 doesn't exist" (operator paged past a root commit)
	// or "this dir has no .git" — both should degrade to an empty list
	// rather than 500 the request. Real git binary failures still surface
	// on subsequent calls (and the launcher logs them via runGit).
	local, _ := gitLogForPlaybook(r.Context(), a.opts.UserPlaybooksDir, relPath, "local", before, limit)
	upstream, _ := gitLogForPlaybook(r.Context(), a.opts.PluginPlaybooksDir, relPath, "upstream", before, limit)
	merged := mergePlaybookCommits(local, upstream, limit)
	writeJSON(w, http.StatusOK, map[string]any{"commits": merged})
}

// mergePlaybookCommits interleaves local + upstream commits by date
// descending, capped at limit.
func mergePlaybookCommits(a, b []playbookCommit, limit int) []playbookCommit {
	out := make([]playbookCommit, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) && len(out) < limit {
		if a[i].Date >= b[j].Date {
			out = append(out, a[i])
			i++
		} else {
			out = append(out, b[j])
			j++
		}
	}
	for i < len(a) && len(out) < limit {
		out = append(out, a[i])
		i++
	}
	for j < len(b) && len(out) < limit {
		out = append(out, b[j])
		j++
	}
	return out
}

// resolveTypeFor finds which type slot the id lives in. User dir wins
// (an override stays in its type slot); system metaCache is the
// fallback. Returns an error when the id is unknown to both — set-active
// / disable on a fresh id makes no sense before it's saved.
func (a *apiHandlers) resolveTypeFor(id string) (string, error) {
	groups, _ := loadUserPlaybookGroups(a.opts.UserPlaybooksDir)
	if g, ok := groups[id]; ok && g.Type != "" {
		return g.Type, nil
	}
	if meta := a.metaCache.get(); meta != nil {
		if mp, ok := meta.Playbooks[id]; ok && mp.Type != "" {
			return mp.Type, nil
		}
	}
	return "", fmt.Errorf("playbook %q not found in user or system set", id)
}
