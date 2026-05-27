package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/sourcehawk/triagent/internal/repos"
)

// handleUpdateRepoSummary accepts an operator-authored markdown body
// for the cache file. Stamps frontmatter to now and tags model:
// operator-edit so a future refresh's success event lands on top of
// it without confusion. Refuses with 409 when a generation is in
// flight — avoids losing the edit to a finishing generation.
//
// PUT /api/repos/{owner}/{name}/summary
// body: {"content": "...", "kind"?: "freeform"}
func (a *apiHandlers) handleUpdateRepoSummary(w http.ResponseWriter, r *http.Request) {
	owner := r.PathValue("owner")
	name := r.PathValue("name")
	if owner == "" || name == "" {
		writeError(w, http.StatusBadRequest, "owner and name are required")
		return
	}
	if a.architectureWorker != nil && a.architectureWorker.IsInFlight(owner, name) {
		writeError(w, http.StatusConflict, "a generation is in flight for this repo; wait for it to finish before editing")
		return
	}

	var body struct {
		Content string `json:"content"`
		Kind    string `json:"kind,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.Content) == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	if body.Kind == "" {
		body.Kind = "freeform"
	}

	now := time.Now().UTC()
	sum := &repos.SummaryFile{
		Frontmatter: repos.SummaryFrontmatter{
			GeneratedAt: now,
			Kind:        body.Kind,
			Model:       "operator-edit",
			ByteCount:   len(body.Content),
		},
		Body: body.Content,
	}
	path := repos.SummaryPath(a.opts.GitCacheDir, owner, name)
	if err := repos.WriteSummary(path, sum); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, repoSummaryStatusResponse{
		Exists:      true,
		GeneratedAt: now.Format(time.RFC3339),
		Kind:        body.Kind,
		ByteCount:   len(body.Content),
	})
}
