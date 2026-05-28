package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sourcehawk/triagent/internal/claude"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// interruptibleSession is a fake investigationSession whose event channel
// only closes when its per-turn ctx is cancelled (or after a long
// timeout). Lets tests start a turn, call Interrupt, and assert that the
// cancellation propagated end-to-end without spawning the real claude
// binary.
type interruptibleSession struct {
	// ctxObserved is closed once Resume/Start has seen its turn ctx —
	// tests wait on this so they call Interrupt only after the turn is
	// actually in flight (otherwise streaming flips back to false
	// before Interrupt runs and the assertion is a race).
	ctxObserved chan struct{}
	// On ctx cancel, the session emits an EventError to mimic claude's
	// "claude exited: exit status 1" path after SIGKILL. drain should
	// suppress this when interrupted is set.
	emitErrorOnCancel bool
}

func newInterruptibleSession() *interruptibleSession {
	return &interruptibleSession{
		ctxObserved:       make(chan struct{}),
		emitErrorOnCancel: true,
	}
}

func (s *interruptibleSession) Start(ctx context.Context) (<-chan claude.Event, error) {
	return s.spawn(ctx), nil
}

func (s *interruptibleSession) Resume(ctx context.Context, _ string) (<-chan claude.Event, error) {
	return s.spawn(ctx), nil
}

func (s *interruptibleSession) spawn(ctx context.Context) <-chan claude.Event {
	ch := make(chan claude.Event, 1)
	go func() {
		defer close(ch)
		close(s.ctxObserved)
		select {
		case <-ctx.Done():
			if s.emitErrorOnCancel {
				ch <- claude.Event{
					Kind: claude.EventError,
					Err:  errors.New("claude exited: signal: killed"),
				}
			}
		case <-time.After(5 * time.Second):
			// Safety net: never block CI on a hung test.
		}
	}()
	return ch
}

func TestInvestigation_Interrupt_NotStreamingReturnsSentinel(t *testing.T) {
	t.Parallel()
	mgr := NewManager(context.Background(), t.TempDir())
	t.Cleanup(mgr.Shutdown)
	inv := mgr.RegisterForTest("inv-int-1")
	inv.mu.Lock()
	inv.started = true
	inv.streaming = false
	inv.mu.Unlock()

	err := inv.Interrupt()
	require.ErrorIs(t, err, ErrNotStreaming, "Interrupt with no in-flight turn must return ErrNotStreaming")
}

func TestInvestigation_Interrupt_CancelsTurnAndEmitsBreadcrumb(t *testing.T) {
	t.Parallel()
	mgr := NewManager(context.Background(), t.TempDir())
	t.Cleanup(mgr.Shutdown)
	inv := mgr.RegisterForTest("inv-int-2")
	sess := newInterruptibleSession()
	inv.mu.Lock()
	inv.session = sess
	inv.started = true
	inv.mu.Unlock()

	// Kick off a turn. Resume goes through SendFollowUp's path so we
	// exercise the same turnCancel wiring that production uses.
	require.NoError(t, inv.SendFollowUp("human", "do the thing"))

	// Wait until the fake session has captured its turn ctx — only
	// then is Interrupt meaningful.
	select {
	case <-sess.ctxObserved:
	case <-time.After(2 * time.Second):
		t.Fatal("session never observed its turn ctx")
	}

	require.NoError(t, inv.Interrupt(), "Interrupt while streaming should succeed")

	// drain runs in a goroutine; wait for streaming to flip back to
	// false (the natural end-of-turn signal).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		inv.mu.Lock()
		s := inv.streaming
		inv.mu.Unlock()
		if !s {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	inv.mu.Lock()
	require.False(t, inv.streaming, "drain should have cleared streaming after the turn ended")
	require.Nil(t, inv.turnCancel, "drain should clear turnCancel after the turn ended")
	require.False(t, inv.interrupted, "drain should clear the interrupted flag after publishing the breadcrumb")
	inv.mu.Unlock()

	// Assert on the published transcript:
	//   - no error envelope (it was an operator-initiated cancel)
	//   - a "(stopped the agent)" breadcrumb (audit trail)
	//   - an end envelope (turn closure)
	events := inv.snapshotEvents()
	var sawError, sawBreadcrumb, sawEnd bool
	for _, e := range events {
		if e.Kind == envKindError {
			sawError = true
		}
		if e.Kind == envKindUser && e.Text == "(stopped the agent)" {
			sawBreadcrumb = true
		}
		if e.Kind == envKindEnd {
			sawEnd = true
		}
	}
	assert.False(t, sawError, "error envelope must be suppressed on interrupt; got transcript: %+v", events)
	assert.True(t, sawBreadcrumb, "expected breadcrumb envelope; got transcript: %+v", events)
	assert.True(t, sawEnd, "expected end envelope; got transcript: %+v", events)
}

// TestInvestigation_Interrupt_LeavesSessionResumable proves the cancel
// is per-turn — after an interrupt the investigation is still 'started'
// and a fresh SendFollowUp on the same session works.
func TestInvestigation_Interrupt_LeavesSessionResumable(t *testing.T) {
	t.Parallel()
	mgr := NewManager(context.Background(), t.TempDir())
	t.Cleanup(mgr.Shutdown)
	inv := mgr.RegisterForTest("inv-int-3")
	sess := newInterruptibleSession()
	inv.mu.Lock()
	inv.session = sess
	inv.started = true
	inv.mu.Unlock()

	require.NoError(t, inv.SendFollowUp("human", "first"))
	select {
	case <-sess.ctxObserved:
	case <-time.After(2 * time.Second):
		t.Fatal("session never observed its turn ctx")
	}
	require.NoError(t, inv.Interrupt())

	// Wait for the turn to fully wind down.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		inv.mu.Lock()
		s := inv.streaming
		inv.mu.Unlock()
		if !s {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Second SendFollowUp on the same session must succeed; the
	// investigation's lifetime ctx is untouched by the per-turn cancel.
	// Use a fresh ctxObserved channel so this second turn doesn't see
	// the already-closed one from the first turn.
	sess.ctxObserved = make(chan struct{})
	require.NoError(t, inv.SendFollowUp("human", "second"), "session should still be resumable after interrupt")
}

func TestHandleInterrupt_NotStreamingReturns409(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mgr := NewManager(context.Background(), root)
	t.Cleanup(mgr.Shutdown)
	inv := mgr.RegisterForTest("inv-int-h-1")
	inv.mu.Lock()
	inv.started = true
	inv.streaming = false
	inv.mu.Unlock()

	a := &apiHandlers{manager: mgr}
	req := httptest.NewRequest(http.MethodPost, "/api/investigations/inv-int-h-1/interrupt", nil)
	req.SetPathValue("id", "inv-int-h-1")
	w := httptest.NewRecorder()
	a.handleInterrupt(w, req)

	require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
}

func TestHandleInterrupt_UnknownInvestigationReturns404(t *testing.T) {
	t.Parallel()
	mgr := NewManager(context.Background(), t.TempDir())
	t.Cleanup(mgr.Shutdown)
	a := &apiHandlers{manager: mgr}
	req := httptest.NewRequest(http.MethodPost, "/api/investigations/missing/interrupt", nil)
	req.SetPathValue("id", "missing")
	w := httptest.NewRecorder()
	a.handleInterrupt(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleInterrupt_StreamingReturns202(t *testing.T) {
	t.Parallel()
	mgr := NewManager(context.Background(), t.TempDir())
	t.Cleanup(mgr.Shutdown)
	inv := mgr.RegisterForTest("inv-int-h-2")
	sess := newInterruptibleSession()
	inv.mu.Lock()
	inv.session = sess
	inv.started = true
	inv.mu.Unlock()
	require.NoError(t, inv.SendFollowUp("human", "do it"))
	select {
	case <-sess.ctxObserved:
	case <-time.After(2 * time.Second):
		t.Fatal("session never observed its turn ctx")
	}

	a := &apiHandlers{manager: mgr}
	req := httptest.NewRequest(http.MethodPost, "/api/investigations/inv-int-h-2/interrupt", nil)
	req.SetPathValue("id", "inv-int-h-2")
	w := httptest.NewRecorder()
	a.handleInterrupt(w, req)
	require.Equal(t, http.StatusAccepted, w.Code, "body: %s", w.Body.String())
}
