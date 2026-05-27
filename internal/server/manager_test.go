package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sourcehawk/triagent/internal/auto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAutoBackend is a no-op autoBackendish used by EnableAuto tests. It
// records the briefing prompt so tests can assert Start dispatched it,
// and exposes a fixed session id so applyAutoState's capture path is
// exercised end-to-end.
type fakeAutoBackend struct {
	mu        sync.Mutex
	prompts   []string
	sessionID string
}

func (f *fakeAutoBackend) Resume(_ context.Context, prompt string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompts = append(f.prompts, prompt)
	return nil
}

func (f *fakeAutoBackend) SessionID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessionID
}

func (f *fakeAutoBackend) seenPrompts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.prompts))
	copy(out, f.prompts)
	return out
}

// TestManager_StartFromWatch_RunsSessionAndOptionalAuto guards the bug
// where watch-spawned investigations sat idle until the operator
// manually opened the SessionView. createFromWatch in server.go used to
// Register the investigation and stop, leaving started=false and
// Auto.Enabled=false even when the watch asked for AutoMode.
// StartFromWatch is the seam createFromWatch now uses to (a) launch the
// main claude session and (b), when autoOpts is non-nil, fire EnableAuto
// in a goroutine so the spawner's slot isn't held by the multi-second
// claude warm-up.
func TestManager_StartFromWatch_RunsSessionAndOptionalAuto(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(context.Background(), root)
	t.Cleanup(mgr.Shutdown)
	inv := mgr.RegisterForTest("inv-wf-auto")
	require.NoError(t, os.MkdirAll(inv.SessionDir, 0o700))

	mcpDir := t.TempDir()
	opMCP := filepath.Join(mcpDir, "op-mcp.json")
	require.NoError(t, os.WriteFile(opMCP, []byte(`{"mcpServers":{}}`), 0o600))

	startCalls := 0
	startFn := func() error {
		startCalls++
		inv.mu.Lock()
		inv.started = true
		inv.mu.Unlock()
		return nil
	}
	fake := &fakeAutoBackend{sessionID: "fake-op"}
	autoOpts := &AutoOptions{
		OperatorMCPConfigPath: opMCP,
		OperatorCwd:           mcpDir,
		Briefing:              "wf briefing",
		BackendFactory: func(_ AutoOptions) (autoBackendish, error) {
			return fake, nil
		},
	}

	mgr.StartFromWatch(inv, startFn, autoOpts)

	require.Equal(t, 1, startCalls, "StartFromWatch must invoke the start seam exactly once")

	// EnableAuto is dispatched in a goroutine. Poll for Auto.Enabled
	// rather than sleeping a fixed interval so the test doesn't race
	// against a slow factory.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		inv.mu.Lock()
		enabled := inv.Auto.Enabled
		inv.mu.Unlock()
		if enabled {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	inv.mu.Lock()
	state := inv.Auto
	inv.mu.Unlock()
	require.True(t, state.Enabled, "Auto.Enabled should flip true after StartFromWatch with autoOpts")
	require.Equal(t, auto.PhaseStarted, state.Phase, "Auto.Phase")
}

// TestManager_StartFromWatch_ManualModeSkipsAuto covers the non-auto
// watch path: a watch with AutoMode=false (or a manual signal start)
// should still get its session started but must NOT enable auto mode.
func TestManager_StartFromWatch_ManualModeSkipsAuto(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(context.Background(), root)
	inv := mgr.RegisterForTest("inv-wf-manual")
	require.NoError(t, os.MkdirAll(inv.SessionDir, 0o700))

	startCalls := 0
	startFn := func() error {
		startCalls++
		inv.mu.Lock()
		inv.started = true
		inv.mu.Unlock()
		return nil
	}

	mgr.StartFromWatch(inv, startFn, nil)

	require.Equal(t, 1, startCalls, "StartFromWatch must invoke the start seam exactly once")
	// Give any rogue EnableAuto goroutine a moment to fire so the
	// negative assertion is meaningful.
	time.Sleep(20 * time.Millisecond)
	inv.mu.Lock()
	enabled := inv.Auto.Enabled
	inv.mu.Unlock()
	require.False(t, enabled, "Auto.Enabled must stay false when autoOpts is nil")
}

// TestManager_StartFromWatch_StartErrorIsLoggedNotPropagated asserts
// that a failing start seam doesn't panic / block subsequent work —
// createFromWatch already returned an invID, so we can't surface the
// error to the caller, but we can keep going. The auto path still
// fires (the operator is independent of the main session's startup
// outcome; if the main session is dead, EnableAuto will fail later
// and that path logs).
func TestManager_StartFromWatch_StartErrorIsLoggedNotPropagated(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(context.Background(), root)
	inv := mgr.RegisterForTest("inv-wf-fail")

	startFn := func() error { return assertableError("boom") }

	// Should not panic.
	mgr.StartFromWatch(inv, startFn, nil)
}

// assertableError is a tiny sentinel-error type used to make the
// "Start fails" case readable in the test above. We don't reuse
// stdlib errors.New here to keep the failure mode obvious to readers.
type assertableError string

func (e assertableError) Error() string { return string(e) }

func TestManager_EnableAuto_StartsAutoOperator(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(context.Background(), root)
	inv := mgr.RegisterForTest("inv-1")
	require.NoError(t, os.MkdirAll(inv.SessionDir, 0o700))

	mcpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(mcpDir, 0o700))
	opMCP := filepath.Join(mcpDir, "op-mcp.json")
	require.NoError(t, os.WriteFile(opMCP, []byte(`{"mcpServers":{}}`), 0o600))

	fake := &fakeAutoBackend{sessionID: "fake-op-session"}
	opts := AutoOptions{
		OperatorMCPConfigPath: opMCP,
		OperatorCwd:           mcpDir,
		Briefing:              "test briefing",
		BackendFactory: func(_ AutoOptions) (autoBackendish, error) {
			return fake, nil
		},
	}
	require.NoError(t, mgr.EnableAuto(inv, opts))

	inv.mu.Lock()
	state := inv.Auto
	inv.mu.Unlock()
	require.True(t, state.Enabled, "Auto.Enabled")
	require.Equal(t, auto.PhaseStarted, state.Phase, "Auto.Phase")
	require.Equal(t, "fake-op-session", state.OperatorSessionID, "OperatorSessionID captured from backend")
	require.Equal(t, []string{"test briefing"}, fake.seenPrompts(), "operator Start was called with the briefing")
}

// TestManager_EnableAuto_PublishesStartedEnvelope guards against a
// regression where EnableAuto flipped inv.Auto to PhaseStarted but did
// not publish an auto_mode_state envelope. The operator's first
// snapshot then observed prevPhase == s.Phase == PhaseStarted and
// skipped publishing too — the frontend's useAutoMode never saw the
// session as auto-enabled, so the banner never appeared.
func TestManager_EnableAuto_PublishesStartedEnvelope(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(context.Background(), root)
	inv := mgr.RegisterForTest("inv-started")
	require.NoError(t, os.MkdirAll(inv.SessionDir, 0o700))

	mcpDir := t.TempDir()
	opMCP := filepath.Join(mcpDir, "op-mcp.json")
	require.NoError(t, os.WriteFile(opMCP, []byte(`{"mcpServers":{}}`), 0o600))

	fake := &fakeAutoBackend{sessionID: "fake-op-session"}
	opts := AutoOptions{
		OperatorMCPConfigPath: opMCP,
		OperatorCwd:           mcpDir,
		Briefing:              "test",
		BackendFactory: func(_ AutoOptions) (autoBackendish, error) {
			return fake, nil
		},
	}
	require.NoError(t, mgr.EnableAuto(inv, opts))

	found := false
	for _, e := range inv.snapshotEvents() {
		if e.Kind == envKindAutoModeState && e.AutoMode != nil && e.AutoMode.Phase == "started" {
			found = true
		}
	}
	require.True(t, found, "expected auto_mode_state{started} envelope after EnableAuto")
}

// Regression: LastSentSeq is owned by the boundary watcher, but
// applyAutoState used to blindly assign the operator's State, zeroing
// LastSentSeq on every phase transition. The next `end` envelope then
// rebuilt a wake diff from seq 1 — replaying the entire transcript to
// the operator. Verify the watcher's value survives operator persists.
func TestManager_ApplyAutoState_PreservesLastSentSeqAcrossOperatorPersists(t *testing.T) {
	mgr := NewManager(context.Background(), t.TempDir())
	inv := mgr.RegisterForTest("inv-last-seq")
	require.NoError(t, os.MkdirAll(inv.SessionDir, 0o700))

	inv.mu.Lock()
	inv.Auto.LastSentSeq = 42
	inv.mu.Unlock()

	// Simulate the operator persisting a phase transition without
	// touching LastSentSeq (the boundary watcher's field).
	mgr.applyAutoState(inv, auto.State{Enabled: true, Phase: auto.PhaseStarted})

	inv.mu.Lock()
	got := inv.Auto.LastSentSeq
	inv.mu.Unlock()
	require.Equal(t, 42, got, "watcher's LastSentSeq must survive operator-driven persists")
}

func TestManager_NotifyAutoResume_BuildsCatchupSpan(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(context.Background(), root)
	inv := mgr.RegisterForTest("inv-2")
	require.NoError(t, os.MkdirAll(inv.SessionDir, 0o700))

	mcpDir := t.TempDir()
	opMCP := filepath.Join(mcpDir, "op-mcp.json")
	require.NoError(t, os.WriteFile(opMCP, []byte(`{"mcpServers":{}}`), 0o600))

	fake := &fakeAutoBackend{sessionID: "fake-op-session"}
	opts := AutoOptions{
		OperatorMCPConfigPath: opMCP,
		OperatorCwd:           mcpDir,
		Briefing:              "kick off",
		BackendFactory: func(_ AutoOptions) (autoBackendish, error) {
			return fake, nil
		},
	}
	require.NoError(t, mgr.EnableAuto(inv, opts))

	// Simulate the human taking over: append envelopes then transition
	// to paused with PausedAtSeq set to the first envelope captured
	// during the take-over. Mirrors the /auto/takeover handler: flip
	// inv.Auto fields under the lock then sync the operator's internal
	// phase via op.Pause so notifyAutoResume's op.Resume finds the
	// expected state.
	inv.publish(EventEnvelope{Kind: envKindUser, Origin: "human", Text: "before-pause"})
	inv.mu.Lock()
	inv.Auto.Phase = auto.PhasePaused
	inv.Auto.PausedAtSeq = inv.nextSeq + 1
	pausedAt := inv.Auto.PausedAtSeq
	op := inv.autoOp
	inv.mu.Unlock()
	require.NotNil(t, op, "EnableAuto did not attach an operator")
	op.Pause(pausedAt)
	inv.publish(EventEnvelope{Kind: envKindUser, Origin: "human", Text: "during-pause"})
	inv.publish(EventEnvelope{Kind: envKindAssistant, Text: "agent reply"})

	// Mirror the /auto/resume handler: flip Phase to Resumed (so
	// applyAutoState doesn't re-emit on the operator's own
	// transition), then dispatch via notifyAutoResume.
	inv.mu.Lock()
	inv.Auto.Phase = auto.PhaseResumed
	inv.mu.Unlock()
	mgr.notifyAutoResume(inv)

	// notifyAutoResume kicks the catch-up Resume in a goroutine.
	deadline := time.After(time.Second)
	for {
		if got := fake.seenPrompts(); len(got) >= 2 {
			require.Contains(t, got[1], "during-pause", "catch-up prompt should contain take-over span")
			require.Contains(t, got[1], "agent reply", "catch-up prompt should include in-pause agent turns")
			return
		}
		select {
		case <-deadline:
			t.Fatalf("notifyAutoResume did not call backend.Resume; got prompts=%v", fake.seenPrompts())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestPublishPushState_ReachesMultiplexStream(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(context.Background(), dir)
	inv := &Investigation{ID: "inv-push", SessionDir: dir, CreatedAt: time.Now().UTC()}
	inv.ctx, inv.cancel = context.WithCancel(context.Background())
	inv.manager = m
	m.byID[inv.ID] = inv

	_, ch, _, cancel := m.SubscribeStream("tab", 0)
	t.Cleanup(cancel)

	startedAt := time.Date(2026, 5, 8, 1, 2, 3, 0, time.UTC)
	inv.publishPushState(PushStatePayload{
		Phase:     "pushing",
		StartedAt: &startedAt,
	})

	select {
	case env := <-ch:
		require.Equal(t, envKindPushState, env.Kind, "Kind")
		require.NotNil(t, env.PushState, "PushState payload missing")
		assert.Equal(t, "pushing", env.PushState.Phase, "Phase")
		assert.True(t, env.PushState.StartedAt != nil && env.PushState.StartedAt.Equal(startedAt), "StartedAt round-trip")
		assert.Equal(t, "inv-push", env.InvestigationID, "InvestigationID")
	case <-time.After(time.Second):
		t.Fatal("subscriber timed out waiting for envelope")
	}
}

func TestInvestigation_Publish_PersistsClaudeSessionID(t *testing.T) {
	dir := t.TempDir()
	st := newStore(dir)
	t.Cleanup(st.close)

	inv := &Investigation{
		ID:         "id1",
		SessionDir: dir,
		CreatedAt:  time.Now().UTC(),
		store:      st,
	}
	// Seed an empty metadata.json so loadInvestigation has something to read.
	require.NoError(t, st.writeMetadata(inv.Snapshot()), "seed metadata")

	// Publish an envelope carrying a session id — first observation.
	inv.publish(EventEnvelope{Kind: envKindSystem, SessionID: "sess-xyz"})

	// loadInvestigation reads back the persisted shape.
	loaded, err := loadInvestigation(dir)
	require.NoError(t, err, "loadInvestigation")
	assert.Equal(t, "sess-xyz", loaded.ClaudeSessionID, "ClaudeSessionID after publish")

	// Publish a second envelope with the same id — should not re-persist.
	inv.publish(EventEnvelope{Kind: envKindAssistant, SessionID: "sess-xyz", Text: "hi"})
	loaded2, _ := loadInvestigation(dir)
	assert.Equal(t, "sess-xyz", loaded2.ClaudeSessionID, "ClaudeSessionID changed unexpectedly")
}

func TestLoadInvestigation_NotArchived_SetsNeedsRehydrate(t *testing.T) {
	dir := t.TempDir()
	st := newStore(dir)
	t.Cleanup(st.close)

	require.NoError(t, st.writeMetadata(InvestigationDTO{
		ID:         "rid",
		SessionDir: dir,
		CreatedAt:  time.Now().UTC(),
		// Archived is false (zero value): this is a previously-live
		// session that was not explicitly archived.
		ClaudeSessionID: "sess-1",
	}), "writeMetadata")

	loaded, err := loadInvestigation(dir)
	require.NoError(t, err, "loadInvestigation")
	require.False(t, loaded.archived, "loaded.archived = true, want false (not explicitly archived)")
	require.True(t, loaded.needsRehydrate, "loaded.needsRehydrate = false, want true")
}

func TestLoadInvestigation_Archived_DoesNotSetNeedsRehydrate(t *testing.T) {
	dir := t.TempDir()
	st := newStore(dir)
	t.Cleanup(st.close)

	require.NoError(t, st.writeMetadata(InvestigationDTO{
		ID:         "rid",
		SessionDir: dir,
		CreatedAt:  time.Now().UTC(),
		Archived:   true, // operator-explicit archive in a prior process
	}), "writeMetadata")

	loaded, err := loadInvestigation(dir)
	require.NoError(t, err, "loadInvestigation")
	require.True(t, loaded.archived, "explicit-archived session lost archived bit on Restore")
	require.False(t, loaded.needsRehydrate, "explicit-archived session must not be flagged for rehydrate")
}

// TestRestore_AssignsContext_AndMarksStarted guards against two bugs
// surfaced when restored (non-archived) sessions hit the auto-Start
// path: a nil per-investigation context (panic in exec.CommandContext)
// and a started=false that lets Investigation.Start run instead of
// short-circuiting so the rehydrate path can do its job.
func TestRestore_AssignsContext_AndMarksStarted(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sess-1")
	require.NoError(t, os.MkdirAll(dir, 0o700), "mkdir")
	st := newStore(dir)
	require.NoError(t, st.writeMetadata(InvestigationDTO{
		ID:         "sess-1",
		SessionDir: dir,
		CreatedAt:  time.Now().UTC(),
		// archived=false (zero value) — the not-explicitly-archived case
	}), "seed metadata")
	st.close()

	m := NewManager(context.Background(), root)
	require.NoError(t, m.Restore(), "Restore")
	inv := m.Get("sess-1")
	require.NotNil(t, inv, "Restore did not load sess-1")
	assert.NotNil(t, inv.ctx, "restored investigation has nil ctx — Start would panic")
	assert.NotNil(t, inv.cancel, "restored investigation has nil cancel — Close would panic")
	assert.True(t, inv.started, "non-archived restored investigation should be marked started=true")
	// And: calling Start on it short-circuits without touching the
	// claude binary or the ctx.
	startErr := inv.Start()
	require.Error(t, startErr, "Start should refuse with 'already started'")
	assert.Contains(t, startErr.Error(), "already started", "Start error message")
}

func TestSnapshot_Resumable(t *testing.T) {
	cases := []struct {
		name     string
		archived bool
		needs    bool
		sid      string
		want     bool
	}{
		{"freshly_restored_with_id", false, true, "sess", true},
		{"archived_blocks_resume", true, true, "sess", false},
		{"missing_session_id", false, true, "", false},
		{"already_rehydrated", false, false, "sess", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inv := &Investigation{
				ID:              "x",
				SessionDir:      t.TempDir(),
				CreatedAt:       time.Now().UTC(),
				ClaudeSessionID: c.sid,
				archived:        c.archived,
				needsRehydrate:  c.needs,
			}
			assert.Equal(t, c.want, inv.Snapshot().Resumable, "Resumable")
		})
	}
}

func TestPublishRehydrateState_ReachesMultiplexStream(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(context.Background(), dir)
	inv := &Investigation{ID: "inv-rehydrate", SessionDir: dir, CreatedAt: time.Now().UTC()}
	inv.ctx, inv.cancel = context.WithCancel(context.Background())
	inv.manager = m
	m.byID[inv.ID] = inv

	_, ch, _, cancel := m.SubscribeStream("tab", 0)
	t.Cleanup(cancel)

	inv.publishRehydrateState(RehydrateStatePayload{Phase: "started"})
	select {
	case env := <-ch:
		require.Equal(t, envKindRehydrateState, env.Kind, "Kind")
		require.NotNil(t, env.RehydrateState, "RehydrateState payload missing")
		assert.Equal(t, "started", env.RehydrateState.Phase, "Phase")
		assert.Equal(t, "inv-rehydrate", env.InvestigationID, "InvestigationID")
	case <-time.After(time.Second):
		t.Fatal("subscriber timed out")
	}
}

func TestRestore_OrphanPushClearsFlag(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "session-1")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	startedAt := time.Now().Add(-time.Minute).UTC()
	meta := persistedMetadata{
		ID:             "orphan",
		SessionDir:     dir,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
		PushInProgress: true,
		PushStartedAt:  &startedAt,
	}
	body, _ := json.Marshal(meta)
	require.NoError(t, os.WriteFile(filepath.Join(dir, fileMetadata), body, 0o600))

	m := NewManager(context.Background(), root)
	require.NoError(t, m.Restore())
	inv := m.Get("orphan")
	require.NotNil(t, inv, "orphan investigation not loaded")
	snap := inv.Snapshot()
	assert.False(t, snap.PushInProgress, "PushInProgress should be cleared after Restore, got true")
	assert.NotEmpty(t, snap.PushError, "PushError should be set, got empty")
	assert.Nil(t, snap.PushStartedAt, "PushStartedAt should be nil after recovery")
}

func TestInvestigation_Publish_ReachesMultiplexStream(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(context.Background(), dir)
	inv := &Investigation{ID: "inv-X", SessionDir: dir, CreatedAt: time.Now().UTC()}
	inv.ctx, inv.cancel = context.WithCancel(context.Background())
	inv.manager = m
	m.byID[inv.ID] = inv

	_, ch, _, cancel := m.SubscribeStream("tab", 0)
	t.Cleanup(cancel)

	inv.publish(EventEnvelope{Kind: envKindAssistant, Text: "hi"})

	select {
	case env := <-ch:
		assert.Equal(t, "inv-X", env.InvestigationID, "InvestigationID")
		assert.Equal(t, "assistant", env.Kind, "Kind")
		assert.Equal(t, "hi", env.Text, "Text")
	case <-time.After(time.Second):
		t.Fatal("multiplex stream did not receive envelope from Investigation.publish")
	}
}

func TestPublishGlobalEvent_ReachesMultiplexStream(t *testing.T) {
	m := NewManager(context.Background(), t.TempDir())
	_, ch, _, cancel := m.SubscribeStream("tab", 0)
	t.Cleanup(cancel)

	m.publishGlobalEvent(GlobalEventEnvelope{
		Kind: globalKindRepoSummaryState,
		RepoSummary: &RepoSummaryStatePayload{
			Owner: "example-org", Name: "example-app", Phase: "started",
		},
	})

	select {
	case env := <-ch:
		assert.Equal(t, globalKindRepoSummaryState, env.Kind, "Kind")
		assert.Empty(t, env.InvestigationID, "global event should have no InvestigationID")
		assert.Empty(t, env.EditorSessionID, "global event should have no EditorSessionID")
		require.NotNil(t, env.RepoSummary, "RepoSummary payload missing")
		assert.Equal(t, "started", env.RepoSummary.Phase, "RepoSummary.Phase")
	case <-time.After(time.Second):
		t.Fatal("multiplex stream did not receive envelope from publishGlobalEvent")
	}
}

func TestPersistOriginatingSignalRoundtrips(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	mgr := NewManager(context.Background(), tmp)
	sessDir := filepath.Join(tmp, "round-trip")
	require.NoError(t, os.MkdirAll(sessDir, 0o700))
	inv := &Investigation{
		ID:                 "round-trip",
		Namespace:          "ns",
		MCPConfigPath:      "/tmp/mcp.json",
		SessionDir:         sessDir,
		CreatedAt:          time.Now().UTC(),
		OriginatingWatchID: "w1",
		OriginatingSignal:  &OriginatingSignal{WatchID: "w1", SignalID: "S1"},
	}
	if _, err := mgr.Register(inv); err != nil {
		t.Fatal(err)
	}
	// Restore in a new manager to exercise the persist path.
	mgr2 := NewManager(context.Background(), tmp)
	if err := mgr2.Restore(); err != nil {
		t.Fatal(err)
	}
	got := mgr2.Get("round-trip")
	if got == nil {
		t.Fatal("investigation not restored")
	}
	if got.OriginatingSignal == nil {
		t.Fatal("OriginatingSignal nil after restore")
	}
	if got.OriginatingSignal.WatchID != "w1" || got.OriginatingSignal.SignalID != "S1" {
		t.Fatalf("unexpected restore: %+v", got.OriginatingSignal)
	}
}
