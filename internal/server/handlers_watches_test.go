package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sourcehawk/triagent/internal/watches"
)

func newTestWatchesAPI(t *testing.T) *apiHandlers {
	t.Helper()
	tmp := t.TempDir()
	reg := watches.NewSourceRegistry()
	reg.Register(stubGHSource{})
	mgr, err := watches.NewManager(watches.ManagerOpts{
		UserWatchesPath: tmp + "/user_watches.yaml",
		Sources:         reg,
		IDGen:           func() string { return "fixed" },
		Create: func(_ context.Context, _ watches.CreateRequest) (string, error) {
			return "INV-X", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr.Start(context.Background())
	t.Cleanup(mgr.Stop)
	return &apiHandlers{watches: mgr}
}

type stubGHSource struct{}

func (stubGHSource) Kind() watches.SourceKind { return watches.SourceGitHubIssues }
func (stubGHSource) Poll(_ context.Context, _ watches.Watch, _ watches.WatermarkState) ([]watches.Item, watches.WatermarkState, error) {
	return nil, watches.WatermarkState{}, nil
}

func TestListWatchesEmpty(t *testing.T) {
	a := newTestWatchesAPI(t)
	r := httptest.NewRequest("GET", "/api/watches", nil)
	w := httptest.NewRecorder()
	a.handleListWatches(w, r)
	if w.Code != 200 {
		t.Fatalf("status=%d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"watches":[]`) {
		t.Fatalf("body=%s, want empty watches array", w.Body.String())
	}
}

func TestCreateWatchSuccess(t *testing.T) {
	a := newTestWatchesAPI(t)
	body, _ := json.Marshal(map[string]any{
		"name":    "n",
		"source":  map[string]any{"kind": "github_issues", "owner": "o", "repo": "r"},
		"polling": map[string]any{"intervalSeconds": 300},
		"enabled": true,
	})
	r := httptest.NewRequest("POST", "/api/watches", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.handleCreateWatch(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d, want 201; body=%s", w.Code, w.Body.String())
	}
	var got watches.Watch
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "fixed" || got.Name != "n" {
		t.Fatalf("unexpected response: %+v", got)
	}
}

func TestCreateWatchRejectsBadRegex(t *testing.T) {
	a := newTestWatchesAPI(t)
	body, _ := json.Marshal(map[string]any{
		"name": "n",
		"source": map[string]any{
			"kind": "github_issues", "owner": "o", "repo": "r",
			"filters": []map[string]any{{"field": "title", "op": "regex_matches", "value": "[broken"}},
		},
		"polling": map[string]any{"intervalSeconds": 300},
	})
	r := httptest.NewRequest("POST", "/api/watches", bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.handleCreateWatch(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", w.Code)
	}
}

func TestPatchWatchRename(t *testing.T) {
	a := newTestWatchesAPI(t)
	w0, _ := a.watches.Create(watches.Watch{Name: "old", Source: watches.SourceConfig{Kind: watches.SourceGitHubIssues, Owner: "o", Repo: "r"}, Polling: watches.PollingConfig{IntervalSeconds: 300}})
	body, _ := json.Marshal(map[string]any{"name": "new"})
	r := httptest.NewRequest("PATCH", "/api/watches/"+w0.ID, bytes.NewReader(body))
	r.SetPathValue("id", w0.ID)
	w := httptest.NewRecorder()
	a.handlePatchWatch(w, r)
	if w.Code != 200 {
		t.Fatalf("status=%d, body=%s", w.Code, w.Body.String())
	}
	var got watches.Watch
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Name != "new" {
		t.Fatalf("name=%q, want new", got.Name)
	}
}

func TestDeleteWatch(t *testing.T) {
	a := newTestWatchesAPI(t)
	w0, _ := a.watches.Create(watches.Watch{Name: "n", Source: watches.SourceConfig{Kind: watches.SourceGitHubIssues, Owner: "o", Repo: "r"}, Polling: watches.PollingConfig{IntervalSeconds: 300}})
	r := httptest.NewRequest("DELETE", "/api/watches/"+w0.ID, nil)
	r.SetPathValue("id", w0.ID)
	w := httptest.NewRecorder()
	a.handleDeleteWatch(w, r)
	if w.Code != 204 {
		t.Fatalf("status=%d", w.Code)
	}
	if len(a.watches.List()) != 0 {
		t.Fatal("watch not removed from manager")
	}
}

func TestListWatchItemsPaginates(t *testing.T) {
	a := newTestWatchesAPI(t)
	w0, _ := a.watches.Create(watches.Watch{Name: "n", Source: watches.SourceConfig{Kind: watches.SourceGitHubIssues, Owner: "o", Repo: "r"}, Polling: watches.PollingConfig{IntervalSeconds: 300}})
	dir := watches.WatchDir(a.watches.UserWatchesPath(), w0.ID)
	for i := 0; i < 5; i++ {
		_ = watches.AppendItem(filepath.Join(dir, "items.jsonl"), watches.Item{ID: fmt.Sprintf("i%d", i), WatchID: w0.ID, CapturedAt: time.Now().UTC().Add(time.Duration(i) * time.Minute), Snapshot: watches.Snapshot{Title: "x"}})
	}
	r := httptest.NewRequest("GET", "/api/watches/"+w0.ID+"/items?limit=2", nil)
	r.SetPathValue("id", w0.ID)
	w := httptest.NewRecorder()
	a.handleListWatchItems(w, r)
	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	var resp struct {
		Items []watches.Item `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Items) != 2 {
		t.Fatalf("got %d items, want 2 (limit)", len(resp.Items))
	}
}

func TestPollNowSynchronous(t *testing.T) {
	a := newTestWatchesAPI(t)
	w0, _ := a.watches.Create(watches.Watch{Name: "n", Source: watches.SourceConfig{Kind: watches.SourceGitHubIssues, Owner: "o", Repo: "r"}, Polling: watches.PollingConfig{IntervalSeconds: 300}, Enabled: true})
	r := httptest.NewRequest("POST", "/api/watches/"+w0.ID+"/poll-now", nil)
	r.SetPathValue("id", w0.ID)
	w := httptest.NewRecorder()
	a.handlePollNowWatch(w, r)
	if w.Code != 200 {
		t.Fatalf("status=%d, body=%s", w.Code, w.Body.String())
	}
	var res watches.PollNowResult
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.DurationMs < 0 {
		t.Fatalf("durationMs should be set: %+v", res)
	}
}

func TestClearWatchItemsOnly(t *testing.T) {
	a := newTestWatchesAPI(t)
	w0, _ := a.watches.Create(watches.Watch{Name: "n", Source: watches.SourceConfig{Kind: watches.SourceGitHubIssues, Owner: "o", Repo: "r"}, Polling: watches.PollingConfig{IntervalSeconds: 300}, Enabled: true})
	dir := watches.WatchDir(a.watches.UserWatchesPath(), w0.ID)
	_ = watches.AppendItem(filepath.Join(dir, "items.jsonl"), watches.Item{ID: "i1", WatchID: w0.ID, CapturedAt: time.Now().UTC()})
	_ = watches.AppendSignal(filepath.Join(dir, "signals.jsonl"), watches.Signal{ID: "S1", WatchID: w0.ID, CreatedAt: time.Now().UTC(), Outcome: watches.OutcomeDisabled})
	body, _ := json.Marshal(map[string]any{"items": true})
	r := httptest.NewRequest("POST", "/api/watches/"+w0.ID+"/clear", bytes.NewReader(body))
	r.SetPathValue("id", w0.ID)
	w := httptest.NewRecorder()
	a.handleClearWatch(w, r)
	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	items, _ := watches.ReadItems(filepath.Join(dir, "items.jsonl"), watches.ReadOpts{Limit: 100, IncludeFiltered: true})
	signals, _ := watches.ReadSignals(filepath.Join(dir, "signals.jsonl"), watches.ReadOpts{Limit: 100})
	if len(items) != 0 || len(signals) != 1 {
		t.Fatalf("expected items wiped, signals kept; got items=%d signals=%d", len(items), len(signals))
	}
}

func TestIngestHistoryReturnsRecentSignals(t *testing.T) {
	a := newTestWatchesAPI(t)
	a.ingestToken = "tok"
	w0, err := a.watches.Create(watches.Watch{
		Name:      "n",
		Source:    watches.SourceConfig{Kind: watches.SourceGitHubIssues, Owner: "o", Repo: "r"},
		Polling:   watches.PollingConfig{IntervalSeconds: 300},
		AutoStart: watches.AutoStartConfig{Enabled: true, MaxConcurrent: 1},
		Enabled:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := watches.WatchDir(a.watches.UserWatchesPath(), w0.ID)
	if err := watches.AppendSignal(filepath.Join(dir, "signals.jsonl"), watches.Signal{
		ID:              "S1",
		WatchID:         w0.ID,
		CreatedAt:       time.Now().UTC(),
		Outcome:         watches.OutcomeInvestigationStarted,
		InvestigationID: "INV1",
	}); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"sinceHours": 72})
	r := httptest.NewRequest("POST", "/api/watches/"+w0.ID+"/ingest/history", bytes.NewReader(body))
	r.SetPathValue("id", w0.ID)
	r.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	a.handleIngestHistory(rec, r)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"signalId":"S1"`) {
		t.Fatalf("expected S1 in body: %s", rec.Body.String())
	}
}

func TestIngestRejectsMissingBearer(t *testing.T) {
	a := newTestWatchesAPI(t)
	a.ingestToken = "tok"
	r := httptest.NewRequest("POST", "/api/watches/x/ingest/history", bytes.NewReader([]byte(`{}`)))
	r.SetPathValue("id", "x")
	// No Authorization header.
	rec := httptest.NewRecorder()
	a.handleIngestHistory(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", rec.Code)
	}
}

func TestIngestStartInvestigationAppendsSignal(t *testing.T) {
	a := newTestWatchesAPI(t)
	a.ingestToken = "tok"
	w0, err := a.watches.Create(watches.Watch{
		Name:      "n",
		Source:    watches.SourceConfig{Kind: watches.SourceGitHubIssues, Owner: "o", Repo: "r"},
		Polling:   watches.PollingConfig{IntervalSeconds: 300},
		Ingest:    watches.IngestConfig{Enabled: true},
		AutoStart: watches.AutoStartConfig{Enabled: true, MaxConcurrent: 1},
		Enabled:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"briefing":     "engine ooms",
		"citedItemIds": []string{"I1"},
		"autoMode":     true,
	})
	r := httptest.NewRequest("POST", "/api/watches/"+w0.ID+"/ingest/start-investigation", bytes.NewReader(body))
	r.SetPathValue("id", w0.ID)
	r.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	a.handleIngestStart(rec, r)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"queued":true`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
	dir := watches.WatchDir(a.watches.UserWatchesPath(), w0.ID)
	deadline := time.Now().Add(time.Second)
	var sigs []watches.Signal
	for time.Now().Before(deadline) {
		sigs, _ = watches.ReadSignals(filepath.Join(dir, "signals.jsonl"), watches.ReadOpts{Limit: 10})
		if len(sigs) >= 1 && sigs[0].Outcome == watches.OutcomeInvestigationStarted && sigs[0].InvestigationID == "INV-X" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(sigs) != 1 || sigs[0].Outcome != watches.OutcomeInvestigationStarted || sigs[0].InvestigationID != "INV-X" {
		t.Fatalf("expected investigation_started/INV-X signal, got %+v", sigs)
	}
	if !strings.Contains(sigs[0].Briefing, "engine ooms") {
		t.Fatalf("briefing not threaded: %+v", sigs[0])
	}
}

func TestIngestReportUnclearAppendsSignal(t *testing.T) {
	a := newTestWatchesAPI(t)
	a.ingestToken = "tok"
	w0, _ := a.watches.Create(watches.Watch{Name: "n", Source: watches.SourceConfig{Kind: watches.SourceGitHubIssues, Owner: "o", Repo: "r"}, Polling: watches.PollingConfig{IntervalSeconds: 300}, Enabled: true})
	body, _ := json.Marshal(map[string]any{"citedItemIds": []string{"I1"}, "reason": "ambiguous"})
	r := httptest.NewRequest("POST", "/api/watches/"+w0.ID+"/ingest/report-unclear", bytes.NewReader(body))
	r.SetPathValue("id", w0.ID)
	r.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	a.handleIngestUnclear(rec, r)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	dir := watches.WatchDir(a.watches.UserWatchesPath(), w0.ID)
	sigs, _ := watches.ReadSignals(filepath.Join(dir, "signals.jsonl"), watches.ReadOpts{Limit: 10})
	if len(sigs) != 1 || sigs[0].Outcome != watches.OutcomeUnclear {
		t.Fatalf("expected one unclear signal, got %+v", sigs)
	}
	if sigs[0].Reason != "ambiguous" {
		t.Fatalf("reason not threaded: %+v", sigs[0])
	}
}

func TestIngestDismissItemsAppendsSignal(t *testing.T) {
	a := newTestWatchesAPI(t)
	a.ingestToken = "tok"
	w0, _ := a.watches.Create(watches.Watch{Name: "n", Source: watches.SourceConfig{Kind: watches.SourceGitHubIssues, Owner: "o", Repo: "r"}, Polling: watches.PollingConfig{IntervalSeconds: 300}, Enabled: true})
	body, _ := json.Marshal(map[string]any{
		"itemIds":            []string{"I1", "I2"},
		"reason":             "duplicate",
		"dismissedWikiSlugs": []string{"keda-cooldown"},
	})
	r := httptest.NewRequest("POST", "/api/watches/"+w0.ID+"/ingest/dismiss-items", bytes.NewReader(body))
	r.SetPathValue("id", w0.ID)
	r.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	a.handleIngestDismiss(rec, r)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	dir := watches.WatchDir(a.watches.UserWatchesPath(), w0.ID)
	sigs, _ := watches.ReadSignals(filepath.Join(dir, "signals.jsonl"), watches.ReadOpts{Limit: 10})
	if len(sigs) != 1 || sigs[0].Outcome != watches.OutcomeDismissed {
		t.Fatalf("expected one dismissed signal, got %+v", sigs)
	}
	if len(sigs[0].DismissedWikiSlugs) != 1 || sigs[0].DismissedWikiSlugs[0] != "keda-cooldown" {
		t.Fatalf("wiki slugs not threaded: %+v", sigs[0])
	}
}

func TestGetWatchQueueEmpty(t *testing.T) {
	a := newTestWatchesAPI(t)
	w0, _ := a.watches.Create(watches.Watch{
		Name:      "n",
		Source:    watches.SourceConfig{Kind: watches.SourceGitHubIssues, Owner: "o", Repo: "r"},
		Polling:   watches.PollingConfig{IntervalSeconds: 300},
		AutoStart: watches.AutoStartConfig{Enabled: true, MaxConcurrent: 3},
		Enabled:   true,
	})
	r := httptest.NewRequest("GET", "/api/watches/"+w0.ID+"/queue", nil)
	r.SetPathValue("id", w0.ID)
	rec := httptest.NewRecorder()
	a.handleGetWatchQueue(rec, r)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"maxConcurrent":3`) {
		t.Fatalf("expected maxConcurrent=3 from watch config; body=%s", body)
	}
}

func TestCancelQueuedNotInQueue(t *testing.T) {
	a := newTestWatchesAPI(t)
	w0, _ := a.watches.Create(watches.Watch{
		Name:    "n",
		Source:  watches.SourceConfig{Kind: watches.SourceGitHubIssues, Owner: "o", Repo: "r"},
		Polling: watches.PollingConfig{IntervalSeconds: 300},
		Enabled: true,
	})
	r := httptest.NewRequest("POST", "/api/watches/"+w0.ID+"/queue/missing/cancel", nil)
	r.SetPathValue("id", w0.ID)
	r.SetPathValue("sid", "missing")
	rec := httptest.NewRecorder()
	a.handleCancelQueued(rec, r)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestManualStartMutatesSignal(t *testing.T) {
	a := newTestWatchesAPI(t)
	// Enabled=false: no poller goroutine; avoids a race with PruneItems.
	w0, _ := a.watches.Create(watches.Watch{
		Name:    "n",
		Source:  watches.SourceConfig{Kind: watches.SourceGitHubIssues, Owner: "o", Repo: "r"},
		Polling: watches.PollingConfig{IntervalSeconds: 300},
	})
	dir := watches.WatchDir(a.watches.UserWatchesPath(), w0.ID)
	if err := watches.AppendSignal(filepath.Join(dir, "signals.jsonl"), watches.Signal{
		ID:           "S1",
		WatchID:      w0.ID,
		CreatedAt:    time.Now().UTC(),
		Outcome:      watches.OutcomeUnclear,
		CitedItemIDs: []string{"I1"},
	}); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"clusters": []string{"prod"}})
	r := httptest.NewRequest("POST", "/api/watches/"+w0.ID+"/signals/S1/start", bytes.NewReader(body))
	r.SetPathValue("id", w0.ID)
	r.SetPathValue("sid", "S1")
	rec := httptest.NewRecorder()
	a.handleManualStart(rec, r)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	sigs, _ := watches.ReadSignals(filepath.Join(dir, "signals.jsonl"), watches.ReadOpts{Limit: 10})
	found := false
	for _, s := range sigs {
		if s.ID == "S1" {
			if s.Outcome != watches.OutcomeInvestigationStarted {
				t.Fatalf("unexpected outcome: %+v", s)
			}
			if !s.ManuallyStarted {
				t.Fatalf("ManuallyStarted not set: %+v", s)
			}
			if s.InvestigationID != "INV-X" {
				t.Fatalf("InvestigationID not set: %+v", s)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("S1 not present after mutation")
	}
}

func TestManualStartMissingSignal(t *testing.T) {
	a := newTestWatchesAPI(t)
	w0, _ := a.watches.Create(watches.Watch{
		Name:    "n",
		Source:  watches.SourceConfig{Kind: watches.SourceGitHubIssues, Owner: "o", Repo: "r"},
		Polling: watches.PollingConfig{IntervalSeconds: 300},
	})
	r := httptest.NewRequest("POST", "/api/watches/"+w0.ID+"/signals/UNKNOWN/start", bytes.NewReader([]byte(`{}`)))
	r.SetPathValue("id", w0.ID)
	r.SetPathValue("sid", "UNKNOWN")
	rec := httptest.NewRecorder()
	a.handleManualStart(rec, r)
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
		t.Fatalf("expected 4xx for missing signal, got %d", rec.Code)
	}
}

func TestRetrySignalRequiresFailed(t *testing.T) {
	a := newTestWatchesAPI(t)
	w0, _ := a.watches.Create(watches.Watch{
		Name:      "n",
		Source:    watches.SourceConfig{Kind: watches.SourceGitHubIssues, Owner: "o", Repo: "r"},
		Polling:   watches.PollingConfig{IntervalSeconds: 300},
		AutoStart: watches.AutoStartConfig{Enabled: true, MaxConcurrent: 1},
	})
	dir := watches.WatchDir(a.watches.UserWatchesPath(), w0.ID)
	// Seed a non-failed signal — retry should refuse.
	if err := watches.AppendSignal(filepath.Join(dir, "signals.jsonl"), watches.Signal{
		ID: "S1", WatchID: w0.ID, CreatedAt: time.Now().UTC(),
		Outcome: watches.OutcomeUnclear, CitedItemIDs: []string{"I1"},
	}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/watches/"+w0.ID+"/signals/S1/retry", nil)
	r.SetPathValue("id", w0.ID)
	r.SetPathValue("sid", "S1")
	rec := httptest.NewRecorder()
	a.handleRetrySignal(rec, r)
	if rec.Code < 400 {
		t.Fatalf("expected 4xx for retry on non-failed signal; got %d", rec.Code)
	}
}
