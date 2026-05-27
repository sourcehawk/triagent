package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleArchive_Idempotent(t *testing.T) {
	a := &apiHandlers{
		manager: NewManager(context.Background(), t.TempDir()),
	}
	inv := &Investigation{ID: "abc", Namespace: "prod"}
	inv2, err := a.manager.Register(inv)
	require.NoError(t, err)

	do := func() int {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/investigations/"+inv2.ID+"/archive", nil)
		req.SetPathValue("id", inv2.ID)
		a.handleArchiveInvestigation(rr, req)
		return rr.Code
	}
	assert.Equal(t, 200, do(), "first call")
	assert.True(t, inv2.Snapshot().Archived, "expected archived=true after first call")
	assert.Equal(t, 200, do(), "second call")
}

func TestHandlePushSessionPR_NotArchived(t *testing.T) {
	a := &apiHandlers{manager: NewManager(context.Background(), t.TempDir())}
	a.opts.SessionsRepo = "owner/repo" // bypass the empty-repo guard; this test exercises the not-archived branch
	inv, _ := a.manager.Register(&Investigation{ID: "abc"})
	_ = inv
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/investigations/abc/push-pr", nil)
	req.SetPathValue("id", "abc")
	a.handlePushSessionPR(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code, "got %d, want 400", rr.Code)
}

func TestHandlePushSessionPR_NotFound(t *testing.T) {
	a := &apiHandlers{manager: NewManager(context.Background(), t.TempDir())}
	a.opts.SessionsRepo = "owner/repo"
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/investigations/missing/push-pr", nil)
	req.SetPathValue("id", "missing")
	a.handlePushSessionPR(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code, "got %d, want 404", rr.Code)
}

func TestHandlePushSessionPR_AlreadyPushed(t *testing.T) {
	a := &apiHandlers{manager: NewManager(context.Background(), t.TempDir())}
	a.opts.SessionsRepo = "owner/repo"
	inv, _ := a.manager.Register(&Investigation{ID: "abc"})
	// Archive it.
	inv.mu.Lock()
	inv.archived = true
	inv.mu.Unlock()
	// Mark as already pushed.
	t2 := time.Now().UTC()
	inv.mu.Lock()
	inv.PushedAt = &t2
	inv.mu.Unlock()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/investigations/abc/push-pr", nil)
	req.SetPathValue("id", "abc")
	a.handlePushSessionPR(rr, req)
	assert.Equal(t, http.StatusConflict, rr.Code, "got %d, want 409", rr.Code)
}

func TestHandlePushSessionPR_KickoffReturns202(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	_, clone := initBareAndClone(t)
	sessionDir := t.TempDir()
	a := &apiHandlers{
		manager:        NewManager(context.Background(), t.TempDir()),
		sessionDrafter: blockUntilCancelledDrafter(t),
	}
	t.Cleanup(a.manager.Shutdown)
	a.opts.SessionsPath = clone
	a.opts.SessionsCloneRoot = clone
	a.opts.SessionsRepo = "owner/repo"
	a.opts.SessionsProposalsPath = t.TempDir()
	a.capabilities = Capabilities{
		GH:       GHStatus{Installed: true, Authenticated: true},
		Sessions: SessionsStatus{Configured: true, Valid: true},
	}
	inv, _ := a.manager.Register(&Investigation{ID: "abc123def456", Namespace: "prod-eu-1", SessionDir: sessionDir})
	// writeTestSessionDir must come after Register so it overwrites the
	// auto-written metadata with the canonical test fixture.
	writeTestSessionDir(t, sessionDir)
	inv.mu.Lock()
	inv.archived = true
	inv.mu.Unlock()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/investigations/abc123def456/push-pr", nil)
	req.SetPathValue("id", "abc123def456")
	a.handlePushSessionPR(rr, req)

	require.Equal(t, http.StatusAccepted, rr.Code, "got %d, want 202", rr.Code)
	time.Sleep(50 * time.Millisecond)
	assert.True(t, inv.Snapshot().PushInProgress, "PushInProgress should be true while goroutine runs")
}

// Regression: archiving via the handler used to call inv.Close(), which
// drained the persist store's writer channel. The push handler then
// fanned out a "pushing" push_state envelope, hit the closed channel,
// and panicked ("send on closed channel"). PushInProgress was already
// committed to disk by that point, so the investigation got stuck at
// "a push is already in progress" forever.
//
// This test archives via the real handler (covers the inv.Close path
// the previous tests sidestepped by toggling inv.archived directly) and
// then immediately pushes. The kickoff must return 202 without
// panicking.
func TestHandlePushSessionPR_AfterArchiveDoesNotPanic(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	_, clone := initBareAndClone(t)
	sessionDir := t.TempDir()
	a := &apiHandlers{
		manager:        NewManager(context.Background(), t.TempDir()),
		sessionDrafter: blockUntilCancelledDrafter(t),
	}
	t.Cleanup(a.manager.Shutdown)
	a.opts.SessionsPath = clone
	a.opts.SessionsCloneRoot = clone
	a.opts.SessionsRepo = "owner/repo"
	a.opts.SessionsProposalsPath = t.TempDir()
	a.capabilities = Capabilities{
		GH:       GHStatus{Installed: true, Authenticated: true},
		Sessions: SessionsStatus{Configured: true, Valid: true},
	}
	inv, _ := a.manager.Register(&Investigation{ID: "abc", Namespace: "prod", SessionDir: sessionDir})
	writeTestSessionDir(t, sessionDir)

	archiveReq := httptest.NewRequest(http.MethodPost, "/api/investigations/"+inv.ID+"/archive", nil)
	archiveReq.SetPathValue("id", inv.ID)
	archiveRR := httptest.NewRecorder()
	a.handleArchiveInvestigation(archiveRR, archiveReq)
	require.Equal(t, http.StatusOK, archiveRR.Code, "archive: got %d, want 200", archiveRR.Code)
	require.True(t, inv.Snapshot().Archived, "archived flag should be set")

	pushReq := httptest.NewRequest(http.MethodPost, "/api/investigations/"+inv.ID+"/push-pr", nil)
	pushReq.SetPathValue("id", inv.ID)
	pushRR := httptest.NewRecorder()
	a.handlePushSessionPR(pushRR, pushReq)

	assert.Equal(t, http.StatusAccepted, pushRR.Code, "push: got %d, want 202", pushRR.Code)
}

func TestHandleArchive_409WhileRehydrating(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(context.Background(), dir)
	inv := &Investigation{
		ID:          "id",
		SessionDir:  dir,
		rehydrating: true,
	}
	inv.ctx, inv.cancel = context.WithCancel(context.Background())
	mgr.byID[inv.ID] = inv
	a := &apiHandlers{manager: mgr}

	req := httptest.NewRequest(http.MethodPost, "/api/investigations/"+inv.ID+"/archive", nil)
	req.SetPathValue("id", inv.ID)
	rr := httptest.NewRecorder()
	a.handleArchiveInvestigation(rr, req)

	assert.Equal(t, http.StatusConflict, rr.Code, "status = %d, want 409", rr.Code)
}

func TestHandlePushSessionPR_ConcurrentKickoffReturns409(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	_, clone := initBareAndClone(t)
	sessionDir := t.TempDir()
	a := &apiHandlers{
		manager:        NewManager(context.Background(), t.TempDir()),
		sessionDrafter: blockUntilCancelledDrafter(t),
	}
	t.Cleanup(a.manager.Shutdown)
	a.opts.SessionsPath = clone
	a.opts.SessionsCloneRoot = clone
	a.opts.SessionsRepo = "owner/repo"
	a.opts.SessionsProposalsPath = t.TempDir()
	a.capabilities = Capabilities{
		GH:       GHStatus{Installed: true, Authenticated: true},
		Sessions: SessionsStatus{Configured: true, Valid: true},
	}
	inv, _ := a.manager.Register(&Investigation{ID: "abc123def456", Namespace: "prod-eu-1", SessionDir: sessionDir})
	// writeTestSessionDir must come after Register so it overwrites the
	// auto-written metadata with the canonical test fixture.
	writeTestSessionDir(t, sessionDir)
	inv.mu.Lock()
	inv.archived = true
	inv.mu.Unlock()

	rr1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/api/investigations/abc123def456/push-pr", nil)
	req1.SetPathValue("id", "abc123def456")
	a.handlePushSessionPR(rr1, req1)
	require.Equal(t, http.StatusAccepted, rr1.Code, "first call got %d, want 202", rr1.Code)
	time.Sleep(50 * time.Millisecond)

	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/investigations/abc123def456/push-pr", nil)
	req2.SetPathValue("id", "abc123def456")
	a.handlePushSessionPR(rr2, req2)
	assert.Equal(t, http.StatusConflict, rr2.Code, "second call got %d, want 409", rr2.Code)
}
