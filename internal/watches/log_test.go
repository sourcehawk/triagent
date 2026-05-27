package watches

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestItem(id string, t time.Time, signalID string) Item {
	return Item{
		ID: id, WatchID: "w1", SourceKind: "github_issues", CapturedAt: t,
		SourceRef: SourceRef{Owner: "o", Repo: "r", IssueNumber: 1},
		Snapshot:  Snapshot{Title: "t", URL: "u"},
		SignalID:  signalID,
	}
}

func TestAppendAndReadItems(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "items.jsonl")
	t0 := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	if err := AppendItem(path, newTestItem("a", t0, "")); err != nil {
		t.Fatal(err)
	}
	if err := AppendItem(path, newTestItem("b", t0.Add(time.Minute), "S1")); err != nil {
		t.Fatal(err)
	}
	items, err := ReadItems(path, ReadOpts{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != "b" || items[1].ID != "a" {
		t.Fatalf("expected newest-first [b,a], got %+v", items)
	}
}

func TestReadItemsBeforeCursor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "items.jsonl")
	t0 := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	for i, id := range []string{"a", "b", "c"} {
		_ = AppendItem(path, newTestItem(id, t0.Add(time.Duration(i)*time.Minute), ""))
	}
	items, _ := ReadItems(path, ReadOpts{Limit: 10, Before: t0.Add(2 * time.Minute)})
	if len(items) != 2 || items[0].ID != "b" || items[1].ID != "a" {
		t.Fatalf("expected [b,a], got %+v", items)
	}
}

func TestReadItemsExcludesFilteredByDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "items.jsonl")
	t0 := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	clean := newTestItem("a", t0, "")
	dirty := newTestItem("b", t0.Add(time.Minute), "")
	dirty.Filtered = &FilteredAnnotation{RuleIndex: 0, Summary: "title does not contain X"}
	_ = AppendItem(path, clean)
	_ = AppendItem(path, dirty)
	got, _ := ReadItems(path, ReadOpts{Limit: 10})
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("expected only clean, got %+v", got)
	}
	all, _ := ReadItems(path, ReadOpts{Limit: 10, IncludeFiltered: true})
	if len(all) != 2 {
		t.Fatalf("expected both with IncludeFiltered, got %d", len(all))
	}
}

func TestPruneItemsByAge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "items.jsonl")
	now := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	_ = AppendItem(path, newTestItem("old", now.Add(-8*24*time.Hour), ""))
	_ = AppendItem(path, newTestItem("fresh", now.Add(-1*time.Hour), ""))
	if err := PruneItems(path, now, 7*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	got, _ := ReadItems(path, ReadOpts{Limit: 10})
	if len(got) != 1 || got[0].ID != "fresh" {
		t.Fatalf("expected only fresh, got %+v", got)
	}
}

func newSignal(id string, t time.Time, outcome Outcome) Signal {
	return Signal{ID: id, WatchID: "w1", CreatedAt: t, Outcome: outcome, CitedItemIDs: []string{"I" + id}}
}

func TestAppendAndReadSignals(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "signals.jsonl")
	t0 := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	_ = AppendSignal(path, newSignal("a", t0, OutcomeDisabled))
	_ = AppendSignal(path, newSignal("b", t0.Add(time.Minute), OutcomeUnclear))
	got, _ := ReadSignals(path, ReadOpts{Limit: 10})
	if len(got) != 2 || got[0].ID != "b" {
		t.Fatalf("expected newest-first [b,a], got %+v", got)
	}
}

func TestSignalMutateViaAppendLatestWins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "signals.jsonl")
	t0 := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	_ = AppendSignal(path, Signal{ID: "S1", WatchID: "w1", CreatedAt: t0, Outcome: OutcomeQueued, CitedItemIDs: []string{"I1"}})
	_ = AppendSignal(path, Signal{ID: "S1", WatchID: "w1", CreatedAt: t0.Add(time.Minute), Outcome: OutcomeInvestigationStarted, InvestigationID: "INV1", CitedItemIDs: []string{"I1"}})
	got, _ := ReadSignals(path, ReadOpts{Limit: 10})
	if len(got) != 1 || got[0].Outcome != OutcomeInvestigationStarted || got[0].InvestigationID != "INV1" {
		t.Fatalf("expected latest row to win, got %+v", got)
	}
}
