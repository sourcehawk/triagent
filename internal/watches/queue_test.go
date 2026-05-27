package watches

import "testing"

func TestQueueRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := QueueState{
		Running: []string{"INV1"},
		Queued:  []QueuedEntry{{SignalID: "S1", EnqueuedAt: "2026-05-12T10:00:00Z"}},
	}
	if err := SaveQueue(dir, in); err != nil {
		t.Fatal(err)
	}
	got, err := LoadQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Running) != 1 || len(got.Queued) != 1 || got.Queued[0].SignalID != "S1" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestLoadQueueMissingFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	q, err := LoadQueue(dir)
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if len(q.Running) != 0 || len(q.Queued) != 0 {
		t.Fatalf("expected empty state, got %+v", q)
	}
}
