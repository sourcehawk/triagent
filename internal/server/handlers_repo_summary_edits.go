package server

import (
	"errors"
	"net/http"

	"github.com/sourcehawk/triagent/internal/repos"
)

// repoSummaryEditsResponse is the shape returned by GET .../summary/edits.
// Mirrors what the frontend's "view edits" UI consumes — hasEdits drives
// the "operator edits • view diff" badge on the repo page; diff carries
// the unified diff content for the modal. Empty diff with hasEdits=false
// means either the active body matches the baseline or no baseline has
// been written yet (no AI regen has happened).
type repoSummaryEditsResponse struct {
	HasEdits bool   `json:"hasEdits"`
	Diff     string `json:"diff,omitempty"`
}

// handleGetRepoSummaryEdits returns the current operator-edit diff
// (active body vs. AI-generated baseline) for a repo. The frontend
// uses this to render the "operator edits" badge + view-diff modal,
// and to decide what to show in the refresh confirm dialog when the
// operator triggers regeneration over a hand-edited summary.
//
// GET /api/repos/{owner}/{name}/summary/edits
func (a *apiHandlers) handleGetRepoSummaryEdits(w http.ResponseWriter, r *http.Request) {
	owner := r.PathValue("owner")
	name := r.PathValue("name")
	if owner == "" || name == "" {
		writeError(w, http.StatusBadRequest, "owner and name are required")
		return
	}

	// Active body comes from the cache file's parsed body — strip
	// frontmatter so the diff isn't polluted by stamp changes.
	activePath := repos.SummaryPath(a.opts.GitCacheDir, owner, name)
	active, err := repos.ReadSummary(activePath)
	if errors.Is(err, repos.ErrSummaryNotFound) {
		// No active summary at all → nothing could have been edited.
		writeJSON(w, http.StatusOK, repoSummaryEditsResponse{HasEdits: false})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	baselinePath := repos.BaselinePath(a.opts.GitCacheDir, owner, name)
	diff, hasEdits, err := repos.OperatorEditsDiff(r.Context(), baselinePath, active.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, repoSummaryEditsResponse{
		HasEdits: hasEdits,
		Diff:     diff,
	})
}
