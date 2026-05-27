package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/sourcehawk/triagent/internal/repos"
)

// repoSummaryResponse is the JSON shape returned by GET .../summary. Mirrors
// the triagent-git getRepoArchitectureSummary tool's output.
type repoSummaryResponse struct {
	Exists      bool   `json:"exists"`
	GeneratedAt string `json:"generatedAt,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Focus       string `json:"focus,omitempty"`
	Content     string `json:"content,omitempty"`
	ByteCount   int    `json:"byteCount,omitempty"`
	Error       string `json:"error,omitempty"`
	Hint        string `json:"hint,omitempty"`
}

// handleGetRepoSummary returns the current cached summary for a linked repo.
// 200 + {exists: false, hint: …} when no cache exists yet — frontend treats
// "not yet generated" as a normal state, not an error, so we don't return 404
// in that case.
//
// GET /api/repos/{owner}/{name}/summary
func (a *apiHandlers) handleGetRepoSummary(w http.ResponseWriter, r *http.Request) {
	owner := r.PathValue("owner")
	name := r.PathValue("name")
	if owner == "" || name == "" {
		writeError(w, http.StatusBadRequest, "owner and name are required")
		return
	}
	path := repos.SummaryPath(a.opts.GitCacheDir, owner, name)
	sum, err := repos.ReadSummary(path)
	if errors.Is(err, repos.ErrSummaryNotFound) {
		writeJSON(w, http.StatusOK, repoSummaryResponse{
			Exists: false,
			Hint:   "No cached architecture summary for this repo yet. Trigger generation from the repo page or wait for auto-gen on connection to complete.",
		})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, repoSummaryResponse{
		Exists:      true,
		GeneratedAt: sum.Frontmatter.GeneratedAt.Format(time.RFC3339),
		Kind:        sum.Frontmatter.Kind,
		Focus:       sum.Frontmatter.Focus,
		Content:     sum.Body,
		ByteCount:   sum.Frontmatter.ByteCount,
		Error:       sum.Frontmatter.Error,
	})
}

// handleRefreshRepoSummary kicks off an async generation. 202 on accepted,
// 409 when one is already in flight for this repo. Body is optional —
// {kind: "freeform", focus: "…"} when set, otherwise defaults.
//
// POST /api/repos/{owner}/{name}/summary/refresh
func (a *apiHandlers) handleRefreshRepoSummary(w http.ResponseWriter, r *http.Request) {
	owner := r.PathValue("owner")
	name := r.PathValue("name")
	if owner == "" || name == "" {
		writeError(w, http.StatusBadRequest, "owner and name are required")
		return
	}
	if a.architectureWorker == nil {
		writeError(w, http.StatusServiceUnavailable, "architecture worker not configured")
		return
	}

	var body struct {
		Kind  string `json:"kind,omitempty"`
		Focus string `json:"focus,omitempty"`
		// IncludeOperatorEdits, when true, asks the launcher to
		// compute the diff between the active body and the
		// AI-generated baseline and pass it to the sub-agent as
		// advisory context. The frontend sets this only after the
		// operator confirmed in the refresh dialog that they want
		// their hand-edits considered. Default false — a fresh
		// regen ignores prior edits.
		IncludeOperatorEdits bool `json:"includeOperatorEdits,omitempty"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	if body.Kind == "" {
		body.Kind = "freeform"
	}

	ctx := context.Background()
	if a.generationContext != nil {
		ctx = a.generationContext()
	}
	// Resolve the operator-authored description so the sub-agent can
	// self-allocate emphasis. Best-effort: a missing repo or read
	// failure leaves description empty; the prompt handles that
	// gracefully (skips the description section).
	description := a.lookupRepoDescription(owner, name)

	// If requested, compute the operator-edits diff so the sub-agent
	// has it as advisory context. Best-effort: a diff failure
	// degrades to "no edits" rather than failing the refresh.
	var editsDiff string
	if body.IncludeOperatorEdits {
		editsDiff = a.computeOperatorEditsDiff(ctx, owner, name)
	}

	ok := a.architectureWorker.GenerateAsync(ctx, owner, name, repos.GenerateRequest{
		Kind:              body.Kind,
		Description:       description,
		Focus:             body.Focus,
		OperatorEditsDiff: editsDiff,
	})
	if !ok {
		writeError(w, http.StatusConflict, "a generation is already in flight for this repo")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// lookupRepoDescription resolves the operator-authored description for
// owner/name by scanning the profile's linked_repos defaults and
// user_repos.yaml. Returns "" when not found or on read error — the
// caller treats description as optional (an absent description means
// the sub-agent runs without operator emphasis hints, identical to
// behaviour for repos added before the description requirement landed).
func (a *apiHandlers) lookupRepoDescription(owner, name string) string {
	for _, d := range repos.ProfileDefaults(a.opts.Profile) {
		if d.Owner == owner && d.Name == name {
			return d.Description
		}
	}
	users, _ := repos.LoadUserRepos(a.opts.UserReposPath)
	for _, u := range users {
		if u.Owner == owner && u.Name == name {
			return u.Description
		}
	}
	return ""
}

// computeOperatorEditsDiff returns the unified diff of the active
// summary body against its AI-generated baseline, or "" when there's
// nothing to surface (no baseline yet, no divergence, or read error).
// Best-effort: a failed read or diff degrades silently to "no
// preserved edits" rather than blocking the refresh.
func (a *apiHandlers) computeOperatorEditsDiff(ctx context.Context, owner, name string) string {
	activePath := repos.SummaryPath(a.opts.GitCacheDir, owner, name)
	active, err := repos.ReadSummary(activePath)
	if err != nil {
		return ""
	}
	baselinePath := repos.BaselinePath(a.opts.GitCacheDir, owner, name)
	diff, hasEdits, err := repos.OperatorEditsDiff(ctx, baselinePath, active.Body)
	if err != nil || !hasEdits {
		return ""
	}
	return diff
}
