package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/sourcehawk/triagent/internal/repos"
)

// repoSummaryStatusResponse is the lightweight status shape — no
// content body, just the metadata the sidenav needs to render its
// status icons. Mirrors RepoSummaryStatePayload's serialization so a
// page can switch between live SSE updates and an initial fetch
// without reshaping data.
type repoSummaryStatusResponse struct {
	Exists      bool   `json:"exists"`
	GeneratedAt string `json:"generatedAt,omitempty"`
	Kind        string `json:"kind,omitempty"`
	ByteCount   int    `json:"byteCount,omitempty"`
	InFlight    bool   `json:"inFlight"`
	Error       string `json:"error,omitempty"`
}

// handleGetRepoSummaryStatus returns frontmatter + in-flight state
// without paying for a full content read. Used by the sidenav at
// mount time to seed its status icons; live updates after that come
// from the global SSE channel.
//
// GET /api/repos/{owner}/{name}/summary/status
func (a *apiHandlers) handleGetRepoSummaryStatus(w http.ResponseWriter, r *http.Request) {
	owner := r.PathValue("owner")
	name := r.PathValue("name")
	if owner == "" || name == "" {
		writeError(w, http.StatusBadRequest, "owner and name are required")
		return
	}

	inFlight := false
	if a.architectureWorker != nil {
		inFlight = a.architectureWorker.IsInFlight(owner, name)
	}

	path := repos.SummaryPath(a.opts.GitCacheDir, owner, name)
	sum, err := repos.ReadSummary(path)
	if errors.Is(err, repos.ErrSummaryNotFound) {
		writeJSON(w, http.StatusOK, repoSummaryStatusResponse{
			Exists:   false,
			InFlight: inFlight,
		})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, repoSummaryStatusResponse{
		Exists:      true,
		GeneratedAt: sum.Frontmatter.GeneratedAt.Format(time.RFC3339),
		Kind:        sum.Frontmatter.Kind,
		ByteCount:   sum.Frontmatter.ByteCount,
		InFlight:    inFlight,
		Error:       sum.Frontmatter.Error,
	})
}
