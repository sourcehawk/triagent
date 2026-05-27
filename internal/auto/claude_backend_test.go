package auto

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeClaudeSession struct {
	start  func(context.Context, string) error
	resume func(context.Context, string) error
	hasID  func() bool
	idFn   func() string
}

func (f *fakeClaudeSession) Start(ctx context.Context, p string) error  { return f.start(ctx, p) }
func (f *fakeClaudeSession) Resume(ctx context.Context, p string) error { return f.resume(ctx, p) }
func (f *fakeClaudeSession) HasSessionID() bool                         { return f.hasID() }
func (f *fakeClaudeSession) SessionID() string                          { return f.idFn() }

func TestClaudeBackend_FirstResumeStartsSession(t *testing.T) {
	var startedWith, resumedWith string
	fake := &fakeClaudeSession{
		start:  func(_ context.Context, prompt string) error { startedWith = prompt; return nil },
		resume: func(_ context.Context, prompt string) error { resumedWith = prompt; return nil },
		hasID:  func() bool { return false },
		idFn:   func() string { return "" },
	}
	b := NewClaudeBackend(fake)
	require.NoError(t, b.Resume(context.Background(), "first"))
	require.Equal(t, "first", startedWith)
	require.Empty(t, resumedWith)

	fake.hasID = func() bool { return true }
	require.NoError(t, b.Resume(context.Background(), "second"))
	require.Equal(t, "second", resumedWith)
}
