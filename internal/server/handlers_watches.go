package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sourcehawk/triagent/internal/watches"
)

func (a *apiHandlers) handleListWatches(w http.ResponseWriter, _ *http.Request) {
	list := a.watches.List()
	if list == nil {
		list = []watches.Watch{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"watches": list})
}

func (a *apiHandlers) handleCreateWatch(w http.ResponseWriter, r *http.Request) {
	var in watches.Watch
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	in.ID = ""
	out, err := a.watches.Create(in)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (a *apiHandlers) handlePatchWatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var p watches.WatchPatch
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	out, err := a.watches.Patch(id, p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *apiHandlers) handleDeleteWatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.watches.Delete(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (a *apiHandlers) handlePollNowWatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := a.watches.Get(id); !ok {
		http.Error(w, "watch not found", http.StatusNotFound)
		return
	}
	// Wait up to 5 minutes for the poll to complete. The manager's
	// PollNow runs the actual work against its parent context, so a
	// timeout here ONLY cancels the HTTP wait — the agent keeps
	// running and signals appear in the runs panel when it finishes.
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	res, err := a.watches.PollNow(ctx, id)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			http.Error(w, "poll-now still running after 5m — watch the ingestion runs panel for the final outcome", http.StatusGatewayTimeout)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *apiHandlers) handleListWatchItems(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := a.watches.Get(id); !ok {
		http.Error(w, "watch not found", http.StatusNotFound)
		return
	}
	opts := parseReadOpts(r)
	path := filepath.Join(watches.WatchDir(a.watches.UserWatchesPath(), id), "items.jsonl")
	items, err := watches.ReadItems(path, opts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if items == nil {
		items = []watches.Item{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *apiHandlers) handleListWatchSignals(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := a.watches.Get(id); !ok {
		http.Error(w, "watch not found", http.StatusNotFound)
		return
	}
	opts := parseReadOpts(r)
	path := filepath.Join(watches.WatchDir(a.watches.UserWatchesPath(), id), "signals.jsonl")
	signals, err := watches.ReadSignals(path, opts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if signals == nil {
		signals = []watches.Signal{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"signals": signals})
}

func parseReadOpts(r *http.Request) watches.ReadOpts {
	o := watches.ReadOpts{Limit: 100}
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 1000 {
			o.Limit = n
		}
	}
	if b := r.URL.Query().Get("before"); b != "" {
		if t, err := time.Parse(time.RFC3339, b); err == nil {
			o.Before = t
		}
	}
	if v := r.URL.Query().Get("includeFiltered"); v == "true" || v == "1" {
		o.IncludeFiltered = true
	}
	return o
}

type clearReq struct {
	Items     bool   `json:"items"`
	Signals   bool   `json:"signals"`
	OlderThan string `json:"olderThan,omitempty"`
}

func (a *apiHandlers) handleClearWatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := a.watches.Get(id); !ok {
		http.Error(w, "watch not found", http.StatusNotFound)
		return
	}
	var in clearReq
	_ = json.NewDecoder(r.Body).Decode(&in)
	if !in.Items && !in.Signals {
		in.Items, in.Signals = true, true
	}
	var dur time.Duration
	if in.OlderThan != "" {
		d, err := parseDurationWithDays(in.OlderThan)
		if err != nil {
			http.Error(w, "olderThan: "+err.Error(), http.StatusBadRequest)
			return
		}
		dur = d
	}
	itemsRm, sigsRm, err := a.watches.ClearLogs(id, watches.ClearOpts{Items: in.Items, Signals: in.Signals, OlderThan: dur}, time.Now().UTC())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"itemsRemoved": itemsRm, "signalsRemoved": sigsRm})
}

func (a *apiHandlers) handleDeleteWatchLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	dir := watches.WatchDir(a.watches.UserWatchesPath(), id)
	if err := os.RemoveAll(dir); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListIngestRuns surfaces the per-watch ingestion-agent run log so
// operators can audit why signals are empty (agent ran but didn't call
// any tools, claude rejected its args, MCP config was wrong, etc.).
// Returns metadata only — fetch the log content via handleGetIngestRun.
func (a *apiHandlers) handleListIngestRuns(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := a.watches.Get(id); !ok {
		http.Error(w, "watch not found", http.StatusNotFound)
		return
	}
	dir := watches.WatchDir(a.watches.UserWatchesPath(), id)
	runs, err := watches.ListIngestRuns(dir, 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if runs == nil {
		runs = []watches.IngestRunListEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (a *apiHandlers) handleGetIngestRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	runID := r.PathValue("runID")
	if _, ok := a.watches.Get(id); !ok {
		http.Error(w, "watch not found", http.StatusNotFound)
		return
	}
	dir := watches.WatchDir(a.watches.UserWatchesPath(), id)
	detail, err := watches.ReadIngestRun(dir, runID)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "run not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// parseDurationWithDays accepts Go's time.ParseDuration plus a "Nd" suffix.
func parseDurationWithDays(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func (a *apiHandlers) handleGetWatchQueue(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snap, err := a.watches.QueueSnapshot(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (a *apiHandlers) handleCancelQueued(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sid := r.PathValue("sid")
	ok, err := a.watches.CancelQueued(id, sid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "not in queue (may have already started)", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *apiHandlers) handleManualStart(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sid := r.PathValue("sid")
	if _, ok := a.watches.Get(id); !ok {
		http.Error(w, "watch not found", http.StatusNotFound)
		return
	}
	var in struct {
		Clusters []string `json:"clusters,omitempty"`
		Repos    []string `json:"repos,omitempty"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	invID, err := a.watches.ManualStartFromSignal(r.Context(), id, sid, in.Clusters, in.Repos)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"signalId": sid, "investigationId": invID, "manuallyStarted": true})
}

func (a *apiHandlers) handleRetrySignal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sid := r.PathValue("sid")
	if _, ok := a.watches.Get(id); !ok {
		http.Error(w, "watch not found", http.StatusNotFound)
		return
	}
	if err := a.watches.RetryFailedSignal(r.Context(), id, sid); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// checkIngestBearer authenticates the ingestion-agent MCP loopback. Empty
// ingestToken disables the channel entirely.
func (a *apiHandlers) checkIngestBearer(r *http.Request) bool {
	if a.ingestToken == "" {
		return false
	}
	return r.Header.Get("Authorization") == "Bearer "+a.ingestToken
}

func (a *apiHandlers) handleIngestHistory(w http.ResponseWriter, r *http.Request) {
	if !a.checkIngestBearer(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	id := r.PathValue("id")
	if _, ok := a.watches.Get(id); !ok {
		http.Error(w, "watch not found", http.StatusNotFound)
		return
	}
	var in struct {
		SinceHours int `json:"sinceHours"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if in.SinceHours < 1 {
		in.SinceHours = 24
	}
	if in.SinceHours > 168 {
		in.SinceHours = 168
	}
	cutoff := time.Now().UTC().Add(-time.Duration(in.SinceHours) * time.Hour)
	path := filepath.Join(watches.WatchDir(a.watches.UserWatchesPath(), id), "signals.jsonl")
	all, _ := watches.ReadSignals(path, watches.ReadOpts{Limit: 1000})
	entries := make([]map[string]any, 0, len(all))
	for _, s := range all {
		if s.CreatedAt.Before(cutoff) {
			continue
		}
		entries = append(entries, map[string]any{
			"signalId":        s.ID,
			"createdAt":       s.CreatedAt.Format(time.RFC3339),
			"outcome":         string(s.Outcome),
			"clusters":        s.Clusters,
			"briefing":        s.Briefing,
			"reason":          s.Reason,
			"investigationId": s.InvestigationID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (a *apiHandlers) handleIngestStart(w http.ResponseWriter, r *http.Request) {
	if !a.checkIngestBearer(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	id := r.PathValue("id")
	if _, ok := a.watches.Get(id); !ok {
		http.Error(w, "watch not found", http.StatusNotFound)
		return
	}
	var in struct {
		Briefing      string   `json:"briefing"`
		CitedItemIDs  []string `json:"citedItemIds"`
		Clusters      []string `json:"clusters,omitempty"`
		SlackChannel  string   `json:"slackChannel,omitempty"`
		IncidentioURL string   `json:"incidentioUrl,omitempty"`
		Repos         []string `json:"repos,omitempty"`
		AutoMode      bool     `json:"autoMode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sigID, _, err := a.watches.SpawnFromSignal(r.Context(), id, watches.SpawnFromSignalReq{
		Briefing:      in.Briefing,
		CitedItemIDs:  in.CitedItemIDs,
		Clusters:      in.Clusters,
		SlackChannel:  in.SlackChannel,
		IncidentioURL: in.IncidentioURL,
		Repos:         in.Repos,
		AutoMode:      in.AutoMode,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"signalId": sigID, "queued": true, "position": 1})
}

func (a *apiHandlers) handleIngestUnclear(w http.ResponseWriter, r *http.Request) {
	a.handleIngestSimpleAck(w, r, watches.OutcomeUnclear, true)
}

func (a *apiHandlers) handleIngestDismiss(w http.ResponseWriter, r *http.Request) {
	a.handleIngestSimpleAck(w, r, watches.OutcomeDismissed, false)
}

// handleIngestSimpleAck is the shared body for report-unclear and dismiss-items.
// citedAsItemIDs=true means the JSON field is "citedItemIds" (unclear's wire
// shape); false means "itemIds" (dismiss's wire shape). Both append a single
// signal with the appropriate outcome and the relevant payload fields.
func (a *apiHandlers) handleIngestSimpleAck(w http.ResponseWriter, r *http.Request, outcome watches.Outcome, citedAsItemIDs bool) {
	if !a.checkIngestBearer(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	id := r.PathValue("id")
	if _, ok := a.watches.Get(id); !ok {
		http.Error(w, "watch not found", http.StatusNotFound)
		return
	}
	var in struct {
		CitedItemIDs       []string `json:"citedItemIds,omitempty"`
		ItemIDs            []string `json:"itemIds,omitempty"`
		Reason             string   `json:"reason"`
		DismissedRelatedTo []string `json:"dismissedRelatedTo,omitempty"`
		DismissedWikiSlugs []string `json:"dismissedWikiSlugs,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	items := in.CitedItemIDs
	if !citedAsItemIDs {
		items = in.ItemIDs
	}
	dir := watches.WatchDir(a.watches.UserWatchesPath(), id)
	sigID := watches.NewSignalULID()
	sig := watches.Signal{
		ID:                 sigID,
		WatchID:            id,
		CreatedAt:          time.Now().UTC(),
		CitedItemIDs:       items,
		Outcome:            outcome,
		Reason:             in.Reason,
		DismissedRelatedTo: in.DismissedRelatedTo,
		DismissedWikiSlugs: in.DismissedWikiSlugs,
	}
	if err := watches.AppendSignal(filepath.Join(dir, "signals.jsonl"), sig); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"signalId": sigID})
}
