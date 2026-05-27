package watches

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSpawnFromSignalEnqueues(t *testing.T) {
	dir := t.TempDir()
	yaml := filepath.Join(dir, "user_watches.yaml")
	reg := NewSourceRegistry()
	reg.Register(&stubSource{})
	mgr, err := NewManager(ManagerOpts{
		UserWatchesPath: yaml,
		Sources:         reg,
		IDGen:           func() string { return "fixed" },
		Create: func(_ context.Context, _ CreateRequest) (string, error) {
			return "INV-X", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr.Start(context.Background())
	defer mgr.Stop()
	w, err := mgr.Create(Watch{
		Name:      "n",
		Source:    SourceConfig{Kind: SourceGitHubIssues, Owner: "o", Repo: "r"},
		Polling:   PollingConfig{IntervalSeconds: 300},
		AutoStart: AutoStartConfig{Enabled: true, MaxConcurrent: 1},
		Enabled:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	sigID, _, err := mgr.SpawnFromSignal(context.Background(), w.ID, SpawnFromSignalReq{
		Briefing:     "b",
		CitedItemIDs: []string{"I1"},
		AutoMode:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Give the spawner a moment to pop and call Create.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		sigs, _ := ReadSignals(filepath.Join(WatchDir(yaml, w.ID), "signals.jsonl"), ReadOpts{Limit: 10})
		for _, s := range sigs {
			if s.ID == sigID && s.Outcome == OutcomeInvestigationStarted && s.InvestigationID == "INV-X" {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	sigs, _ := ReadSignals(filepath.Join(WatchDir(yaml, w.ID), "signals.jsonl"), ReadOpts{Limit: 10})
	t.Fatalf("expected signal %s to mutate to investigation_started/INV-X; got %+v", sigID, sigs)
}

func TestManagerStartLoadsAndSpawns(t *testing.T) {
	dir := t.TempDir()
	yaml := filepath.Join(dir, "user_watches.yaml")
	w := Watch{ID: "w1", Name: "n", Source: SourceConfig{Kind: SourceGitHubIssues, Owner: "o", Repo: "r"}, Polling: PollingConfig{IntervalSeconds: 300}, Enabled: true}
	if err := SaveWatches(yaml, []Watch{w}); err != nil {
		t.Fatal(err)
	}
	reg := NewSourceRegistry()
	reg.Register(&stubSource{items: []Item{}})
	mgr, err := NewManager(ManagerOpts{
		UserWatchesPath: yaml,
		Sources:         reg,
		Ingestor:        NoOpIngestor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)
	defer mgr.Stop()
	got := mgr.List()
	if len(got) != 1 || got[0].ID != "w1" {
		t.Fatalf("expected w1 in list, got %+v", got)
	}
}

func TestManagerCreate(t *testing.T) {
	dir := t.TempDir()
	yaml := filepath.Join(dir, "user_watches.yaml")
	mgr, _ := NewManager(ManagerOpts{
		UserWatchesPath: yaml,
		Sources:         registryWithStub(),
		IDGen:           func() string { return "fixed-id" },
	})
	mgr.Start(context.Background())
	defer mgr.Stop()
	in := Watch{Name: "n", Source: SourceConfig{Kind: SourceGitHubIssues, Owner: "o", Repo: "r"}, Polling: PollingConfig{IntervalSeconds: 300}, Enabled: true}
	got, err := mgr.Create(in)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "fixed-id" || got.CreatedAt.IsZero() {
		t.Fatalf("Create did not set id/createdAt: %+v", got)
	}
	if len(mgr.List()) != 1 {
		t.Fatalf("expected 1 watch, got %d", len(mgr.List()))
	}
}

func TestManagerPatchRestartsPoller(t *testing.T) {
	dir := t.TempDir()
	yaml := filepath.Join(dir, "user_watches.yaml")
	w := Watch{ID: "w1", Name: "n", Source: SourceConfig{Kind: SourceGitHubIssues, Owner: "o", Repo: "r"}, Polling: PollingConfig{IntervalSeconds: 300}, Enabled: true}
	_ = SaveWatches(yaml, []Watch{w})
	mgr, _ := NewManager(ManagerOpts{UserWatchesPath: yaml, Sources: registryWithStub()})
	mgr.Start(context.Background())
	defer mgr.Stop()
	got, err := mgr.Patch("w1", WatchPatch{Name: strPtr("renamed")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "renamed" {
		t.Fatalf("Patch did not update name: %+v", got)
	}
}

func TestManagerDelete(t *testing.T) {
	dir := t.TempDir()
	yaml := filepath.Join(dir, "user_watches.yaml")
	w := Watch{ID: "w1", Name: "n", Source: SourceConfig{Kind: SourceGitHubIssues, Owner: "o", Repo: "r"}, Polling: PollingConfig{IntervalSeconds: 300}, Enabled: true}
	_ = SaveWatches(yaml, []Watch{w})
	mgr, _ := NewManager(ManagerOpts{UserWatchesPath: yaml, Sources: registryWithStub()})
	mgr.Start(context.Background())
	defer mgr.Stop()
	if err := mgr.Delete("w1"); err != nil {
		t.Fatal(err)
	}
	if len(mgr.List()) != 0 {
		t.Fatalf("expected 0 watches after delete")
	}
}

func registryWithStub() *SourceRegistry {
	r := NewSourceRegistry()
	r.Register(&stubSource{items: []Item{}})
	return r
}

func strPtr(s string) *string { return &s }

func TestManagerPollNow(t *testing.T) {
	dir := t.TempDir()
	yaml := filepath.Join(dir, "user_watches.yaml")
	w := Watch{ID: "w1", Name: "n", Source: SourceConfig{Kind: SourceGitHubIssues, Owner: "o", Repo: "r"}, Polling: PollingConfig{IntervalSeconds: 300}, Enabled: true}
	_ = SaveWatches(yaml, []Watch{w})
	reg := NewSourceRegistry()
	reg.Register(&stubSource{items: []Item{{ID: "a", WatchID: "w1", SourceKind: "github_issues", CapturedAt: time.Now().UTC(), Snapshot: Snapshot{Title: "x"}}}})
	mgr, _ := NewManager(ManagerOpts{UserWatchesPath: yaml, Sources: reg})
	mgr.Start(context.Background())
	defer mgr.Stop()
	time.Sleep(50 * time.Millisecond)
	res, err := mgr.PollNow(context.Background(), "w1")
	if err != nil {
		t.Fatal(err)
	}
	if res.ItemsCaptured != 1 {
		t.Fatalf("PollNow itemsCaptured=%d, want 1", res.ItemsCaptured)
	}
}

func TestNotifyTerminalReleasesSpawnerSlot(t *testing.T) {
	dir := t.TempDir()
	yaml := filepath.Join(dir, "user_watches.yaml")
	reg := NewSourceRegistry()
	reg.Register(&stubSource{})

	// A Create stub that records the create order and blocks until
	// signalled — so we can verify the second signal waits for the
	// first's terminal release.
	created := make(chan string, 8)
	release := make(chan struct{})
	createFn := func(_ context.Context, req CreateRequest) (string, error) {
		created <- req.SignalID
		<-release
		return "INV-" + req.SignalID, nil
	}

	mgr, err := NewManager(ManagerOpts{
		UserWatchesPath: yaml,
		Sources:         reg,
		IDGen:           func() string { return "fixed" },
		Create:          createFn,
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr.Start(context.Background())
	defer mgr.Stop()

	w, err := mgr.Create(Watch{
		Name:      "n",
		Source:    SourceConfig{Kind: SourceGitHubIssues, Owner: "o", Repo: "r"},
		Polling:   PollingConfig{IntervalSeconds: 300},
		AutoStart: AutoStartConfig{Enabled: true, MaxConcurrent: 1},
		Enabled:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Enqueue two signals back-to-back.
	sig1, _, _ := mgr.SpawnFromSignal(context.Background(), w.ID, SpawnFromSignalReq{Briefing: "first", CitedItemIDs: []string{"I1"}, AutoMode: true})
	_, _, _ = mgr.SpawnFromSignal(context.Background(), w.ID, SpawnFromSignalReq{Briefing: "second", CitedItemIDs: []string{"I2"}, AutoMode: true})

	// First should be in-flight (Create called); second should still be queued.
	select {
	case s := <-created:
		if s != sig1 {
			t.Fatalf("expected first create to be %s, got %s", sig1, s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first create never called")
	}
	select {
	case s := <-created:
		t.Fatalf("second signal should be queued, not in-flight; got %s", s)
	case <-time.After(100 * time.Millisecond):
		// good: still queued
	}

	// Release the first; it completes, slot is released.
	close(release)
	// Wait for the first's create to return + the wrapper to log INV-S1 as running.
	// Then notify terminal.
	time.Sleep(50 * time.Millisecond)
	mgr.NotifyTerminal(w.ID, "INV-"+sig1)

	// Second signal's create should now fire.
	select {
	case <-created:
		// success
	case <-time.After(5 * time.Second):
		t.Fatal("second signal did not pop after NotifyTerminal")
	}
}

func TestRehydrateOrphanItems(t *testing.T) {
	dir := t.TempDir()
	yaml := filepath.Join(dir, "user_watches.yaml")
	// Rehydrate only runs for ingest-enabled watches — that's the only
	// mode where items can become orphaned (the trivial-passthrough
	// path is atomic per item).
	w := Watch{ID: "w1", Name: "n", Source: SourceConfig{Kind: SourceGitHubIssues, Owner: "o", Repo: "r"}, Polling: PollingConfig{IntervalSeconds: 300}, Ingest: IngestConfig{Enabled: true}, Enabled: true}
	_ = SaveWatches(yaml, []Watch{w})
	wDir := WatchDir(yaml, "w1")
	prePoll := time.Now().UTC().Add(-2 * time.Minute)
	_ = AppendItem(filepath.Join(wDir, "items.jsonl"), Item{ID: "i1", WatchID: "w1", SourceKind: "github_issues", CapturedAt: prePoll, Snapshot: Snapshot{Title: "a"}})
	_ = AppendItem(filepath.Join(wDir, "items.jsonl"), Item{ID: "i2", WatchID: "w1", SourceKind: "github_issues", CapturedAt: prePoll, Snapshot: Snapshot{Title: "b"}})
	_ = SaveWatermark(filepath.Join(wDir, "state.json"), WatermarkState{LastPolledAt: time.Now().UTC().Add(-1 * time.Minute)})
	mgr, _ := NewManager(ManagerOpts{UserWatchesPath: yaml, Sources: registryWithStub()})
	if err := mgr.RehydrateOrphans(); err != nil {
		t.Fatal(err)
	}
	sigs, _ := ReadSignals(filepath.Join(wDir, "signals.jsonl"), ReadOpts{Limit: 10})
	if len(sigs) != 1 || sigs[0].Outcome != OutcomeDismissed {
		t.Fatalf("expected 1 dismissed orphan-recovery signal, got %+v", sigs)
	}
}

func TestSpawnedBriefingHasAutoTriggerPrefix(t *testing.T) {
	dir := t.TempDir()
	yaml := filepath.Join(dir, "user_watches.yaml")
	reg := NewSourceRegistry()
	reg.Register(&stubSource{})
	captured := make(chan CreateRequest, 1)
	mgr, err := NewManager(ManagerOpts{
		UserWatchesPath: yaml,
		Sources:         reg,
		IDGen:           func() string { return "fixed" },
		Create: func(_ context.Context, cr CreateRequest) (string, error) {
			captured <- cr
			return "INV-X", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr.Start(context.Background())
	defer mgr.Stop()
	w, _ := mgr.Create(Watch{
		Name:      "c1 triage",
		Source:    SourceConfig{Kind: SourceGitHubIssues, Owner: "o", Repo: "r"},
		Polling:   PollingConfig{IntervalSeconds: 300},
		AutoStart: AutoStartConfig{Enabled: true, MaxConcurrent: 1},
		Enabled:   true,
	})
	_, _, err = mgr.SpawnFromSignal(context.Background(), w.ID, SpawnFromSignalReq{
		Briefing:     "Engine OOMs after upgrade.",
		Clusters:     []string{"prod-emea-1"},
		CitedItemIDs: []string{"I1"},
		AutoMode:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case cr := <-captured:
		if !strings.Contains(cr.Briefing, "Auto-triggered by signal-watch ingestion") {
			t.Errorf("briefing missing prefix: %q", cr.Briefing)
		}
		if !strings.Contains(cr.Briefing, "c1 triage") {
			t.Errorf("briefing missing watch name: %q", cr.Briefing)
		}
		if !strings.Contains(cr.Briefing, "prod-emea-1") {
			t.Errorf("briefing missing suggested cluster: %q", cr.Briefing)
		}
		if !strings.Contains(cr.Briefing, "Engine OOMs after upgrade.") {
			t.Errorf("original briefing dropped: %q", cr.Briefing)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Create was never invoked")
	}
}

// TestAutoStart_RetriesOnFailure asserts the spawner retries a failing
// create up to autoStartMaxAttempts and writes a single OutcomeFailed
// row (with errorMessage) only after the last attempt. While retries are
// pending the signal row stays "queued" so the UI doesn't flash failed
// states mid-retry.
func TestAutoStart_RetriesOnFailure(t *testing.T) {
	prev := autoStartBackoff
	autoStartBackoff = func(int) time.Duration { return 10 * time.Millisecond }
	defer func() { autoStartBackoff = prev }()

	dir := t.TempDir()
	yaml := filepath.Join(dir, "user_watches.yaml")
	reg := NewSourceRegistry()
	reg.Register(&stubSource{})

	attempts := make(chan int, autoStartMaxAttempts+1)
	mgr, err := NewManager(ManagerOpts{
		UserWatchesPath: yaml,
		Sources:         reg,
		IDGen:           func() string { return "fixed" },
		Create: func(_ context.Context, cr CreateRequest) (string, error) {
			attempts <- cr.Attempt
			return "", errFakeTransient
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr.Start(context.Background())
	defer mgr.Stop()
	w, _ := mgr.Create(Watch{
		Name:      "n",
		Source:    SourceConfig{Kind: SourceGitHubIssues, Owner: "o", Repo: "r"},
		Polling:   PollingConfig{IntervalSeconds: 300},
		AutoStart: AutoStartConfig{Enabled: true, MaxConcurrent: 1},
		Enabled:   true,
	})
	sigID, _, err := mgr.SpawnFromSignal(context.Background(), w.ID, SpawnFromSignalReq{
		Briefing:     "b",
		CitedItemIDs: []string{"I1"},
	})
	if err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= autoStartMaxAttempts; i++ {
		select {
		case got := <-attempts:
			if got != i {
				t.Fatalf("attempt %d: got cr.Attempt=%d", i, got)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("attempt %d never fired", i)
		}
	}

	deadline := time.Now().Add(time.Second)
	var final Signal
	for time.Now().Before(deadline) {
		sigs, _ := ReadSignals(filepath.Join(WatchDir(yaml, w.ID), "signals.jsonl"), ReadOpts{Limit: 10})
		for _, s := range sigs {
			if s.ID == sigID {
				final = s
				break
			}
		}
		if final.Outcome == OutcomeFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if final.Outcome != OutcomeFailed {
		t.Fatalf("expected final outcome failed, got %q", final.Outcome)
	}
	if !strings.Contains(final.ErrorMessage, "after 4 attempts") {
		t.Fatalf("errorMessage missing attempt count: %q", final.ErrorMessage)
	}
}

// TestAutoStart_RetriesPersistAcrossRestart verifies that queued retry
// state (Attempts, NotBefore) survives a manager restart and that the
// reconstructed spawner resumes from where the previous run left off
// — picking up at attempt 2 when one attempt has already failed.
func TestAutoStart_RetriesPersistAcrossRestart(t *testing.T) {
	prev := autoStartBackoff
	// Long backoff during mgr1's life so the second attempt doesn't
	// fire before the test can Stop. Restored to short delays before
	// mgr2 starts so the resumed retry doesn't take forever.
	autoStartBackoff = func(int) time.Duration { return 5 * time.Second }
	defer func() { autoStartBackoff = prev }()

	dir := t.TempDir()
	yaml := filepath.Join(dir, "user_watches.yaml")
	reg := NewSourceRegistry()
	reg.Register(&stubSource{})

	firstFail := make(chan struct{}, 1)
	mgr1, err := NewManager(ManagerOpts{
		UserWatchesPath: yaml,
		Sources:         reg,
		IDGen:           func() string { return "fixed" },
		Create: func(_ context.Context, _ CreateRequest) (string, error) {
			firstFail <- struct{}{}
			return "", errFakeTransient
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx1, cancel1 := context.WithCancel(context.Background())
	mgr1.Start(ctx1)
	w, _ := mgr1.Create(Watch{
		Name:      "n",
		Source:    SourceConfig{Kind: SourceGitHubIssues, Owner: "o", Repo: "r"},
		Polling:   PollingConfig{IntervalSeconds: 300},
		AutoStart: AutoStartConfig{Enabled: true, MaxConcurrent: 1},
		Enabled:   true,
	})
	sigID, _, err := mgr1.SpawnFromSignal(ctx1, w.ID, SpawnFromSignalReq{
		Briefing:     "b",
		CitedItemIDs: []string{"I1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstFail:
	case <-time.After(5 * time.Second):
		t.Fatal("first attempt never fired")
	}
	cancel1()
	mgr1.Stop()

	wDir := WatchDir(yaml, w.ID)
	deadline := time.Now().Add(time.Second)
	var qs QueueState
	for time.Now().Before(deadline) {
		qs, _ = LoadQueue(wDir)
		if len(qs.Queued) == 1 && qs.Queued[0].Attempts == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(qs.Queued) != 1 || qs.Queued[0].SignalID != sigID || qs.Queued[0].Attempts != 1 {
		t.Fatalf("expected one queued retry with Attempts=1, got %+v", qs)
	}

	// Rewrite NotBefore so mgr2 doesn't wait the full backoff window.
	qs.Queued[0].NotBefore = time.Now().UTC().Format(time.RFC3339)
	if err := SaveQueue(wDir, qs); err != nil {
		t.Fatal(err)
	}
	autoStartBackoff = func(int) time.Duration { return 10 * time.Millisecond }

	captured := make(chan CreateRequest, 4)
	mgr2, err := NewManager(ManagerOpts{
		UserWatchesPath: yaml,
		Sources:         reg,
		Create: func(_ context.Context, cr CreateRequest) (string, error) {
			captured <- cr
			return "INV-RESUME", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer func() {
		cancel2()
		mgr2.Stop()
		// Small drain window so spawner goroutines (running with ctx2)
		// finish their final SaveQueue writes before TempDir cleanup.
		time.Sleep(50 * time.Millisecond)
	}()
	mgr2.Start(ctx2)
	select {
	case cr := <-captured:
		if cr.Attempt != 2 {
			t.Fatalf("resumed at attempt %d, want 2", cr.Attempt)
		}
		if cr.SignalID != sigID {
			t.Fatalf("resumed with wrong signalID: %q", cr.SignalID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("retry never resumed after restart")
	}
}

var errFakeTransient = errFake{"transient boom"}

type errFake struct{ msg string }

func (e errFake) Error() string { return e.msg }

// On launcher restart, a watch whose queue.json is "Running != [] but
// Queued == []" used to skip spawner construction (the gate was
// "Queued > 0" only). If the in-flight investigations were then archived
// while the spawner was absent, NotifyTerminal would no-op and the
// Running list would leak forever — when the next signal arrived and a
// fresh spawner was built lazily, it inherited the stale Running and
// refused to pop. Manager.Start must construct the spawner whenever
// either Queued or Running is non-empty, then reconcile via IsLive so
// stale slots clear on startup.
func TestManagerStartReconcilesStaleRunningWithoutQueued(t *testing.T) {
	dir := t.TempDir()
	yaml := filepath.Join(dir, "user_watches.yaml")
	w := Watch{
		ID:        "w1",
		Name:      "n",
		Source:    SourceConfig{Kind: SourceGitHubIssues, Owner: "o", Repo: "r"},
		Polling:   PollingConfig{IntervalSeconds: 300},
		AutoStart: AutoStartConfig{Enabled: true, MaxConcurrent: 2},
		Enabled:   true,
	}
	if err := SaveWatches(yaml, []Watch{w}); err != nil {
		t.Fatal(err)
	}
	wDir := WatchDir(yaml, "w1")
	if err := SaveQueue(wDir, QueueState{Running: []string{"DEAD-A", "DEAD-B"}}); err != nil {
		t.Fatal(err)
	}

	reg := NewSourceRegistry()
	reg.Register(&stubSource{})
	mgr, err := NewManager(ManagerOpts{
		UserWatchesPath: yaml,
		Sources:         reg,
		IsLive:          func(string) bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr.Start(context.Background())
	defer mgr.Stop()

	// Give Start's goroutines a moment to settle.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		q, _ := LoadQueue(wDir)
		if len(q.Running) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	q, _ := LoadQueue(wDir)
	t.Fatalf("expected stale Running cleared on Start; got %+v", q.Running)
}
