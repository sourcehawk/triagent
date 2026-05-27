package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// POST /api/investigations/{id}/archive
//
// Idempotent: re-archiving is a 200 no-op. Calls Investigation.stopLive() to
// stop streaming, flips archived=true, persists metadata. Gates the
// push-to-upstream button on the frontend. The persistence store stays
// open so subsequent push_state / pr_state / label envelopes still land
// on disk for transcript replay.
func (a *apiHandlers) handleArchiveInvestigation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inv := a.manager.Get(id)
	if inv == nil {
		writeError(w, http.StatusNotFound, "investigation not found")
		return
	}
	inv.mu.Lock()
	if inv.rehydrating {
		inv.mu.Unlock()
		writeError(w, http.StatusConflict, "rehydrate in progress; retry after the rehydrate event resolves")
		return
	}
	alreadyArchived := inv.archived
	inv.mu.Unlock()
	if !alreadyArchived {
		inv.stopLive()
		inv.mu.Lock()
		inv.archived = true
		inv.mu.Unlock()
		if err := a.manager.persistMetadata(inv); err != nil {
			writeError(w, http.StatusInternalServerError, "persist: "+err.Error())
			return
		}
		a.manager.fireTerminal(inv)       // M6.4: release spawner slot on archive
		a.manager.FireCloseHook(inv.ID) // release per-investigation resources (prom port-forward, ...)
	}
	writeJSON(w, http.StatusOK, inv.Snapshot())
}

// POST /api/investigations/{id}/push-pr
//
// Kicks off the push pipeline on a goroutine and returns 202 with the
// investigation's current snapshot (which now has PushInProgress=true).
// Result lands on the existing per-investigation SSE stream as a
// push_state envelope. Synchronous failures (not archived, already
// pushed, already in progress, missing session dir) still return 4xx.
func (a *apiHandlers) handlePushSessionPR(w http.ResponseWriter, r *http.Request) {
	if !a.requireUpstreamRepo(w, "sessions", a.opts.SessionsRepo) {
		return
	}
	id := r.PathValue("id")
	inv := a.manager.Get(id)
	if inv == nil {
		writeError(w, http.StatusNotFound, "investigation not found")
		return
	}

	inv.mu.Lock()
	archived := inv.archived
	pushedAt := inv.PushedAt
	prState := inv.PRState
	sessionDir := inv.SessionDir
	inv.mu.Unlock()

	if !archived {
		writeError(w, http.StatusBadRequest, "investigation must be archived before pushing")
		return
	}
	if pushedAt != nil && prState != PRStateClosed {
		writeError(w, http.StatusConflict, "session already pushed")
		return
	}
	if sessionDir == "" {
		writeError(w, http.StatusInternalServerError, "session dir unknown")
		return
	}

	var req sessionPushRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "decode body: "+err.Error())
			return
		}
	}

	now := time.Now().UTC()
	inv.mu.Lock()
	if inv.PushInProgress {
		inv.mu.Unlock()
		writeError(w, http.StatusConflict, "a push is already in progress for this investigation")
		return
	}
	inv.PushInProgress = true
	inv.PushStartedAt = &now
	inv.PushError = ""
	inv.mu.Unlock()
	_ = a.manager.persistMetadata(inv)
	inv.publishPushState(PushStatePayload{Phase: "pushing", StartedAt: &now})

	go a.runPushSessionPR(inv, sessionDir, req)

	writeJSON(w, http.StatusAccepted, inv.Snapshot())
}

// runPushSessionPR is the goroutine body. It uses the manager's parent
// context (NOT the HTTP request, NOT inv.ctx — both have been cancelled
// by the time push runs) so a client disconnect does not kill the
// triagent-mcp drafter mid-flight, and the goroutine still dies cleanly on
// launcher shutdown.
func (a *apiHandlers) runPushSessionPR(inv *Investigation, sessionDir string, req sessionPushRequest) {
	ctx := a.manager.ParentContext()
	res, errs, err := pushSessionPR(ctx, a.capabilities,
		a.opts.SessionsCloneRoot,
		a.opts.SessionsPath,
		a.opts.SessionsRepo,
		a.opts.SessionsProposalsPath,
		sessionDir, req, a.sessionDrafter)

	now := time.Now().UTC()
	if err != nil {
		inv.mu.Lock()
		inv.PushInProgress = false
		inv.PushStartedAt = nil
		inv.PushError = err.Error()
		inv.mu.Unlock()
		_ = a.manager.persistMetadata(inv)
		inv.publishPushState(PushStatePayload{Phase: "error", Error: err.Error()})
		return
	}
	if len(errs) > 0 {
		joined := strings.Join(errs, " · ")
		inv.mu.Lock()
		inv.PushInProgress = false
		inv.PushStartedAt = nil
		inv.PushError = joined
		inv.mu.Unlock()
		_ = a.manager.persistMetadata(inv)
		inv.publishPushState(PushStatePayload{Phase: "error", Error: joined})
		return
	}

	inv.mu.Lock()
	inv.PushInProgress = false
	inv.PushStartedAt = nil
	inv.PushError = ""
	inv.PushedAt = &now
	inv.PushURL = res.URL
	inv.PRState = PRStateOpen
	inv.PRMergedAt = nil
	inv.PRClosedAt = nil
	inv.mu.Unlock()
	_ = a.manager.persistMetadata(inv)
	inv.publishPushState(PushStatePayload{
		Phase:  "success",
		URL:    res.URL,
		Branch: res.Branch,
		Base:   res.Base,
	})
}
