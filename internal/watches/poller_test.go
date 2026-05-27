package watches

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

type stubSource struct {
	items []Item
	wm    WatermarkState
}

func (s *stubSource) Kind() SourceKind { return SourceGitHubIssues }
func (s *stubSource) Poll(_ context.Context, _ Watch, _ WatermarkState) ([]Item, WatermarkState, error) {
	return s.items, s.wm, nil
}

func TestPollOnceAutoStartOff(t *testing.T) {
	dir := t.TempDir()
	w := Watch{ID: "w1", Name: "n", Source: SourceConfig{Kind: SourceGitHubIssues, Owner: "o", Repo: "r"}, Polling: PollingConfig{IntervalSeconds: 300}, AutoStart: AutoStartConfig{Enabled: false}, Enabled: true}
	src := &stubSource{items: []Item{
		{ID: "a", WatchID: "w1", SourceKind: "github_issues", CapturedAt: time.Now().UTC(), Snapshot: Snapshot{Title: "x"}},
		{ID: "b", WatchID: "w1", SourceKind: "github_issues", CapturedAt: time.Now().UTC(), Snapshot: Snapshot{Title: "y"}},
	}}
	p := NewPoller(w, dir, src, NoOpIngestor{}, NopPublisher{})
	if err := p.pollOnce(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	items, _ := ReadItems(filepath.Join(dir, "items.jsonl"), ReadOpts{Limit: 10})
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	sigs, _ := ReadSignals(filepath.Join(dir, "signals.jsonl"), ReadOpts{Limit: 10})
	if len(sigs) != 2 {
		t.Fatalf("expected 2 trivial signals (autoStart=off), got %d", len(sigs))
	}
	for _, s := range sigs {
		if s.Outcome != OutcomeDisabled {
			t.Errorf("signal %s outcome=%s, want disabled", s.ID, s.Outcome)
		}
	}
}

func TestPollOnceAppliesFilters(t *testing.T) {
	dir := t.TempDir()
	w := Watch{
		ID:        "w1",
		Name:      "n",
		Enabled:   true,
		Source:    SourceConfig{Kind: SourceGitHubIssues, Owner: "o", Repo: "r", Filters: []Filter{{Field: "title", Op: "contains", Value: "OOM"}}},
		Polling:   PollingConfig{IntervalSeconds: 300},
		AutoStart: AutoStartConfig{Enabled: false},
	}
	src := &stubSource{items: []Item{
		{ID: "a", WatchID: "w1", SourceKind: "github_issues", CapturedAt: time.Now().UTC(), Snapshot: Snapshot{Title: "Engine OOMs"}},
		{ID: "b", WatchID: "w1", SourceKind: "github_issues", CapturedAt: time.Now().UTC(), Snapshot: Snapshot{Title: "Docs typo"}},
	}}
	p := NewPoller(w, dir, src, NoOpIngestor{}, NopPublisher{})
	if err := p.pollOnce(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	items, _ := ReadItems(filepath.Join(dir, "items.jsonl"), ReadOpts{Limit: 10, IncludeFiltered: true})
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	// items are newest-first; find by ID.
	for _, it := range items {
		if it.ID == "b" && it.Filtered == nil {
			t.Fatal("item b (no 'OOM') should be marked filtered")
		}
		if it.ID == "a" && it.Filtered != nil {
			t.Fatal("item a (matches) should NOT be filtered")
		}
	}
	sigs, _ := ReadSignals(filepath.Join(dir, "signals.jsonl"), ReadOpts{Limit: 10})
	if len(sigs) != 1 {
		t.Fatalf("expected 1 trivial signal (only non-filtered item produces one), got %d", len(sigs))
	}
}
