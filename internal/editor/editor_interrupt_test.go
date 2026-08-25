package editor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sourcehawk/triagent/internal/claude"
	"github.com/sourcehawk/triagent/prompts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// interruptibleConversation is a fake conversation whose event channel
// only closes when its per-turn ctx is cancelled (or after a safety
// timeout), so tests can start a turn, call Interrupt, and observe the
// cancellation end-to-end without spawning the claude binary.
type interruptibleConversation struct {
	ctxObserved chan struct{}
}

func newInterruptibleConversation() *interruptibleConversation {
	return &interruptibleConversation{ctxObserved: make(chan struct{})}
}

func (c *interruptibleConversation) Start(ctx context.Context, _ string) (<-chan claude.Event, error) {
	return c.spawn(ctx), nil
}

func (c *interruptibleConversation) Resume(ctx context.Context, _ string) (<-chan claude.Event, error) {
	return c.spawn(ctx), nil
}

func (c *interruptibleConversation) spawn(ctx context.Context) <-chan claude.Event {
	ch := make(chan claude.Event, 1)
	go func() {
		defer close(ch)
		close(c.ctxObserved)
		select {
		case <-ctx.Done():
			ch <- claude.Event{Kind: claude.EventError, Err: errors.New("claude exited: signal: killed")}
		case <-time.After(5 * time.Second):
		}
	}()
	return ch
}

func newStartedSession(t *testing.T, conv conversation) *Session {
	t.Helper()
	mgr := NewManager(context.Background(), t.TempDir())
	sess, err := mgr.Register(&Session{Subject: prompts.PlaybookSubject{ID: "p", Version: "HEAD"}})
	require.NoError(t, err)
	sess.mu.Lock()
	sess.inner = conv
	sess.started = true
	sess.mu.Unlock()
	return sess
}

func waitIdle(t *testing.T, sess *Session) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		sess.mu.Lock()
		streaming := sess.streaming
		sess.mu.Unlock()
		if !streaming {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("session never stopped streaming")
}

func TestSession_Interrupt_NotStreamingReturnsSentinel(t *testing.T) {
	t.Parallel()
	sess := newStartedSession(t, newInterruptibleConversation())
	require.ErrorIs(t, sess.Interrupt(), ErrNotStreaming)
}

func TestSession_Interrupt_CancelsTurnAndEmitsBreadcrumb(t *testing.T) {
	t.Parallel()
	conv := newInterruptibleConversation()
	sess := newStartedSession(t, conv)

	require.NoError(t, sess.SendFollowUp("hello"))
	select {
	case <-conv.ctxObserved:
	case <-time.After(2 * time.Second):
		t.Fatal("turn never started")
	}

	require.NoError(t, sess.Interrupt())
	waitIdle(t, sess)

	events, _ := sess.TranscriptSnapshot()
	var kinds []string
	for _, ev := range events {
		kinds = append(kinds, ev.Kind)
	}
	assert.NotContains(t, kinds, KindError, "operator-initiated cancel must not surface as an error")
	assert.Equal(t, []string{KindUser, KindUser, KindEnd}, kinds)
	assert.Equal(t, "(stopped the agent)", events[1].Text)

	// The session itself survives: a follow-up turn works again.
	sess.mu.Lock()
	sess.inner = newInterruptibleConversation()
	sess.mu.Unlock()
	require.NoError(t, sess.SendFollowUp("again"))
	require.ErrorContains(t, sess.SendFollowUp("busy"), "streaming")
}
