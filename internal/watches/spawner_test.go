package watches

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeCreator struct {
	mu    sync.Mutex
	calls []string
	next  func(req CreateRequest) (string, error)
}

func (f *fakeCreator) Create(_ context.Context, req CreateRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req.SignalID)
	if f.next != nil {
		return f.next(req)
	}
	return "INV-" + req.SignalID, nil
}

func (f *fakeCreator) callsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

func TestSpawnerRespectsMaxConcurrent(t *testing.T) {
	dir := t.TempDir()
	f := &fakeCreator{}
	sp := NewSpawner(dir, "w1", 1, f.Create)
	sp.Enqueue(CreateRequest{WatchID: "w1", SignalID: "S1", Briefing: "briefing1", AutoMode: true})
	sp.Enqueue(CreateRequest{WatchID: "w1", SignalID: "S2", Briefing: "briefing2", AutoMode: true})
	ctx, cancel := context.WithCancel(context.Background())
	go sp.Run(ctx)

	// Wait for S1 to start; should be 1 running, 1 queued.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(f.callsSnapshot()) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	calls := f.callsSnapshot()
	if len(calls) != 1 || calls[0] != "S1" {
		t.Fatalf("expected only S1 spawned; calls=%+v", calls)
	}

	// Release the slot; S2 should now pop.
	sp.OnInvestigationTerminal("INV-S1")
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(f.callsSnapshot()) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	calls = f.callsSnapshot()
	if len(calls) != 2 || calls[1] != "S2" {
		t.Fatalf("expected S2 after slot released; calls=%+v", calls)
	}

	cancel()
	// Give the Run loop a moment to exit before we read queue.json.
	time.Sleep(50 * time.Millisecond)
	q, _ := LoadQueue(filepath.Join(dir))
	if len(q.Queued) != 0 {
		t.Fatalf("queue not drained on terminate; %+v", q)
	}
}

func TestSpawnerCancelRemovesEntry(t *testing.T) {
	dir := t.TempDir()
	f := &fakeCreator{}
	sp := NewSpawner(dir, "w1", 1, f.Create)
	sp.Enqueue(CreateRequest{WatchID: "w1", SignalID: "S1", Briefing: "b1", AutoMode: true})
	sp.Enqueue(CreateRequest{WatchID: "w1", SignalID: "S2", Briefing: "b2", AutoMode: true})
	if !sp.Cancel("S2") {
		t.Fatal("expected cancel to succeed for queued S2")
	}
	snap := sp.Snapshot()
	if len(snap.Queued) != 1 || snap.Queued[0].SignalID != "S1" {
		t.Fatalf("expected only S1 queued after cancel; got %+v", snap)
	}
	if sp.Cancel("S99") {
		t.Fatal("cancel of unknown signal should fail")
	}
}

// Slots leak when a watch-spawned investigation reaches a terminal state
// while the Spawner doesn't exist (post-launcher-restart, empty queue,
// lazy construction skipped). The next signal that arrives constructs a
// fresh Spawner from disk and inherits the stale Running list, blocking
// the queue indefinitely. NewSpawner must reconcile Running against the
// IsLive callback so stale invIDs are dropped on construction.
func TestSpawnerReconcilesStaleRunningOnConstruction(t *testing.T) {
	dir := t.TempDir()
	if err := SaveQueue(dir, QueueState{
		Running: []string{"DEAD-A", "DEAD-B"},
		Queued:  []QueuedEntry{{SignalID: "S1", EnqueuedAt: time.Now().UTC().Format(time.RFC3339)}},
	}); err != nil {
		t.Fatal(err)
	}
	isLive := func(string) bool { return false }
	f := &fakeCreator{}
	sp := NewSpawner(dir, "w1", 2, f.Create, WithIsLive(isLive))
	snap := sp.Snapshot()
	if len(snap.Running) != 0 {
		t.Fatalf("expected Running drained on construction; got %+v", snap.Running)
	}
	q, _ := LoadQueue(dir)
	if len(q.Running) != 0 {
		t.Fatalf("expected on-disk Running drained on construction; got %+v", q.Running)
	}
}

// During normal operation, IsLive may flip to false for a slot the
// spawner thinks is in use (e.g. archive happened while the spawner
// was idle and the terminal hook lost the race). The next kick must
// reconcile so queued signals can flow even if NotifyTerminal was
// missed.
func TestSpawnerReconcilesStaleRunningOnKick(t *testing.T) {
	dir := t.TempDir()
	if err := SaveQueue(dir, QueueState{
		Running: []string{"DEAD-A"},
	}); err != nil {
		t.Fatal(err)
	}
	dead := map[string]bool{"DEAD-A": true}
	var mu sync.Mutex
	isLive := func(id string) bool {
		mu.Lock()
		defer mu.Unlock()
		return !dead[id]
	}
	f := &fakeCreator{}
	sp := NewSpawner(dir, "w1", 1, f.Create, WithIsLive(isLive))
	// Sanity: construction already reconciled.
	if r := sp.Snapshot().Running; len(r) != 0 {
		t.Fatalf("expected NewSpawner to clear DEAD-A; got %+v", r)
	}
	// Re-introduce a stale entry behind the spawner's back (simulating
	// a prior terminal-hook miss that wasn't seen on construction).
	sp.mu.Lock()
	sp.state.Running = []string{"DEAD-A"}
	_ = SaveQueue(dir, sp.state)
	sp.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sp.Run(ctx)
	sp.Enqueue(CreateRequest{WatchID: "w1", SignalID: "S1", Briefing: "b", AutoMode: true})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(f.callsSnapshot()) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	calls := f.callsSnapshot()
	if len(calls) != 1 || calls[0] != "S1" {
		t.Fatalf("expected S1 to pop after stale slot reconciled; calls=%+v", calls)
	}
}

// IsLive is optional. When nil, the Spawner must preserve the existing
// behaviour (no reconciliation) — both for compatibility with tests
// that don't wire the seam and so stale slots can't be cleared by a
// caller that hasn't opted in.
func TestSpawnerWithoutIsLiveKeepsRunning(t *testing.T) {
	dir := t.TempDir()
	if err := SaveQueue(dir, QueueState{
		Running: []string{"STILL-ALIVE"},
	}); err != nil {
		t.Fatal(err)
	}
	f := &fakeCreator{}
	sp := NewSpawner(dir, "w1", 1, f.Create)
	if r := sp.Snapshot().Running; len(r) != 1 || r[0] != "STILL-ALIVE" {
		t.Fatalf("expected Running preserved without IsLive; got %+v", r)
	}
}
