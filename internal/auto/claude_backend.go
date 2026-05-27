package auto

import (
	"context"

	"github.com/sourcehawk/triagent/internal/claude"
)

// claudeSession is the slice of claude.Session the backend needs. Defined
// here as an interface so tests can fake it without dragging the real
// claude package in.
type claudeSession interface {
	Start(ctx context.Context, prompt string) error
	Resume(ctx context.Context, prompt string) error
	HasSessionID() bool
	SessionID() string
}

// ClaudeBackend adapts a claudeSession into the Operator's Backend
// contract. On the first Resume call it routes to Start (no session id
// yet); subsequent calls route to the underlying Resume.
type ClaudeBackend struct {
	s claudeSession
}

// NewClaudeBackend wraps a claudeSession into a Backend. The session is
// driven synchronously: Resume returns when the claude process exits (its
// event channel closes). Event handling (publishing to the investigation
// transcript) happens in T17 via the onEvent callback registered on the
// adapter passed in — not in this type.
func NewClaudeBackend(s claudeSession) *ClaudeBackend { return &ClaudeBackend{s: s} }

// Resume picks Start on the first call (no session id yet), Resume after.
// The investigation-side claude.Session has the same shape; we wrap it so
// the Operator doesn't know about the start/resume distinction.
func (b *ClaudeBackend) Resume(ctx context.Context, prompt string) error {
	if !b.s.HasSessionID() {
		return b.s.Start(ctx, prompt)
	}
	return b.s.Resume(ctx, prompt)
}

// SessionID exposes the underlying claude session id once Start has
// captured one. Empty before the first successful Start. The Operator's
// persister reads this after every Resume() and writes it into
// State.OperatorSessionID so a launcher restart can --resume the same
// session.
func (b *ClaudeBackend) SessionID() string { return b.s.SessionID() }

// NewFromClaude wraps a *claude.Session with the synchronous-error
// adapter the Operator expects. Production callers in T17 use this; tests
// use NewClaudeBackend directly with a fake.
func NewFromClaude(s *claude.Session, onEvent func(claude.Event)) *ClaudeBackend {
	return NewClaudeBackend(&claudeSessionAdapter{s: s, onEvent: onEvent})
}

// claudeSessionAdapter wraps the channel-returning Start/Resume into the
// synchronous error-returning interface our claudeSession expects. The
// onEvent callback receives every Event in order; it's the boundary
// between this package and the server package's publish pipeline.
type claudeSessionAdapter struct {
	s       *claude.Session
	onEvent func(claude.Event)
}

func (a *claudeSessionAdapter) Start(ctx context.Context, prompt string) error {
	ch, err := a.s.Start(ctx, prompt)
	if err != nil {
		return err
	}
	a.drain(ch)
	return nil
}

func (a *claudeSessionAdapter) Resume(ctx context.Context, prompt string) error {
	ch, err := a.s.Resume(ctx, prompt)
	if err != nil {
		return err
	}
	a.drain(ch)
	return nil
}

func (a *claudeSessionAdapter) HasSessionID() bool { return a.s.HasSessionID() }
func (a *claudeSessionAdapter) SessionID() string  { return a.s.SessionID() }

func (a *claudeSessionAdapter) drain(ch <-chan claude.Event) {
	for ev := range ch {
		if a.onEvent != nil {
			a.onEvent(ev)
		}
	}
}
