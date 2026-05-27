package auto

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeBackend struct {
	mu      sync.Mutex
	prompts []string
}

func (f *fakeBackend) Resume(_ context.Context, prompt string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompts = append(f.prompts, prompt)
	return nil
}

func (f *fakeBackend) gotPrompts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.prompts))
	copy(out, f.prompts)
	return out
}

func TestAutoOperator_Start_SetsPhaseStarted(t *testing.T) {
	op := newTestOperator(t, &fakeBackend{})
	err := op.Start(context.Background(), "briefing here")
	require.NoError(t, err)
	require.Equal(t, PhaseStarted, op.Phase())
}

func TestAutoOperator_Pause_FromStarted(t *testing.T) {
	op := newTestOperator(t, &fakeBackend{})
	require.NoError(t, op.Start(context.Background(), "x"))
	op.Pause(10)
	require.Equal(t, PhasePaused, op.Phase())
	require.Equal(t, 10, op.PausedAtSeq())
}

func TestAutoOperator_Finish_IsTerminal(t *testing.T) {
	op := newTestOperator(t, &fakeBackend{})
	require.NoError(t, op.Start(context.Background(), "x"))
	op.Finish("done")
	op.Finish("done again")
	require.Equal(t, PhaseFinished, op.Phase())
}

func TestComposeDiff_ExcludesToolResultBlobs(t *testing.T) {
	events := []EventLite{
		{Seq: 1, Kind: "assistant", Text: "looking at logs"},
		{Seq: 2, Kind: "tool_use", ToolName: "get_logs"},
		{Seq: 3, Kind: "tool_result", Text: "<10kb blob>"},
		{Seq: 4, Kind: "assistant", Text: "found cause"},
		{Seq: 5, Kind: "result"},
	}
	prompt := ComposeDiffPrompt(events)
	require.Contains(t, prompt, "looking at logs")
	require.Contains(t, prompt, "get_logs")
	require.Contains(t, prompt, "found cause")
	require.NotContains(t, prompt, "<10kb blob>")
}

func TestComposeCatchupPrompt_PrependsTakeoverNote(t *testing.T) {
	events := []EventLite{
		{Seq: 5, Kind: "user", Origin: "human", Text: "try the broker pods"},
		{Seq: 6, Kind: "assistant", Text: "checking broker"},
	}
	prompt := ComposeCatchupPrompt(events)
	require.True(t, strings.HasPrefix(prompt, "While you were paused, the human took over."))
	require.Contains(t, prompt, "try the broker pods")
}

func TestOperator_Wake_SendsDiffToBackend(t *testing.T) {
	back := &fakeBackend{}
	op := newTestOperator(t, back)
	require.NoError(t, op.Start(context.Background(), "briefing"))
	_ = op.Wake(context.Background(), []EventLite{
		{Seq: 2, Kind: "assistant", Text: "hello"},
	})
	prompts := back.gotPrompts()
	require.Len(t, prompts, 2)
	require.Contains(t, prompts[1], "hello")
}

func TestWake_PrependsSoftCapNotice(t *testing.T) {
	back := &fakeBackend{}
	op := newTestOperator(t, back)
	require.NoError(t, op.Start(context.Background(), "x"))
	for i := 0; i < SoftWakeCap; i++ {
		_ = op.Wake(context.Background(), []EventLite{{Seq: i, Kind: "assistant", Text: "tick"}})
	}
	_ = op.Wake(context.Background(), []EventLite{{Seq: 99, Kind: "assistant", Text: "more"}})
	prompts := back.gotPrompts()
	last := prompts[len(prompts)-1]
	require.Contains(t, last, "consider calling `finish` soon")
}

func TestWake_AbortsAtHardCap(t *testing.T) {
	back := &fakeBackend{}
	op := newTestOperator(t, back)
	require.NoError(t, op.Start(context.Background(), "x"))
	for i := 0; i < HardWakeCap; i++ {
		_ = op.Wake(context.Background(), []EventLite{{Seq: i, Kind: "assistant", Text: "tick"}})
	}
	err := op.Wake(context.Background(), []EventLite{{Seq: 9999, Kind: "assistant", Text: "stop"}})
	require.Equal(t, ErrWakeCapExceeded, err)
	require.Equal(t, PhaseAborted, op.Phase())
}

func TestWake_TwoConsecutiveFailures_Aborts(t *testing.T) {
	back := &failingBackend{}
	op := newTestOperator(t, back)
	_ = op.Start(context.Background(), "x")
	_ = op.Wake(context.Background(), []EventLite{{Kind: "assistant", Text: "hi"}})
	require.Equal(t, PhaseAborted, op.Phase())
}

type failingBackend struct{}

func (failingBackend) Resume(context.Context, string) error { return errors.New("boom") }

func newTestOperator(t *testing.T, b Backend) *Operator {
	t.Helper()
	return New(Config{
		Backend:   b,
		PersistFn: func(State) {},
		Now:       func() time.Time { return time.Unix(0, 0) },
	})
}
