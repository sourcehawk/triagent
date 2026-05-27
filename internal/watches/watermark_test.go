package watches

import (
	"path/filepath"
	"testing"
	"time"
)

func TestWatermarkRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	in := WatermarkState{
		LastPolledAt: time.Date(2026, 5, 11, 10, 5, 0, 0, time.UTC),
		GitHubUpdatedAt: time.Date(2026, 5, 11, 10, 4, 1, 0, time.UTC),
		SeenIssueNumbers: []int{4421, 4422},
	}
	if err := SaveWatermark(path, in); err != nil {
		t.Fatal(err)
	}
	got, err := LoadWatermark(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastPolledAt.Equal(in.LastPolledAt) || len(got.SeenIssueNumbers) != 2 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestLoadWatermarkMissingReturnsZero(t *testing.T) {
	got, err := LoadWatermark(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastPolledAt.IsZero() {
		t.Fatalf("expected zero state, got %+v", got)
	}
}

func TestWatermarkBoundedSeenSet(t *testing.T) {
	st := WatermarkState{}
	for i := 0; i < 300; i++ {
		st = RememberSeenIssue(st, i)
	}
	if len(st.SeenIssueNumbers) != SeenSetMax {
		t.Fatalf("expected at most %d remembered, got %d", SeenSetMax, len(st.SeenIssueNumbers))
	}
	last := st.SeenIssueNumbers[len(st.SeenIssueNumbers)-1]
	if last != 299 {
		t.Fatalf("expected last entry to be 299, got %d", last)
	}
}
