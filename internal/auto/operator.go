package auto

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	SoftWakeCap = 30
	HardWakeCap = 60
)

var ErrWakeCapExceeded = errors.New("auto-mode wake cap exceeded")

// Backend is the surface the AutoOperator drives. In production this wraps a
// claude.Session; in tests we use a fake.
type Backend interface {
	Resume(ctx context.Context, prompt string) error
}

// Config wires the AutoOperator's collaborators.
type Config struct {
	Backend   Backend
	PersistFn func(State)      // called on every transition
	Now       func() time.Time // injectable clock; nil → time.Now
}

// Operator is the runtime AutoOperator.
type Operator struct {
	mu      sync.Mutex
	state   State
	backend Backend
	persist func(State)
	now     func() time.Time
}

func New(cfg Config) *Operator {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.PersistFn == nil {
		cfg.PersistFn = func(State) {}
	}
	return &Operator{
		backend: cfg.Backend,
		persist: cfg.PersistFn,
		now:     cfg.Now,
		state:   State{Enabled: true},
	}
}

// Start spawns the operator agent with the given initial briefing prompt.
func (o *Operator) Start(ctx context.Context, briefing string) error {
	o.mu.Lock()
	o.state.Phase = PhaseStarted
	o.mu.Unlock()
	o.snapshot()
	if err := o.backend.Resume(ctx, briefing); err != nil {
		o.recordFailure()
		return err
	}
	o.recordSuccess()
	return nil
}

// Pause moves to PhasePaused, records the boundary seq for catch-up.
func (o *Operator) Pause(atSeq int) {
	o.mu.Lock()
	if o.state.Phase == PhaseFinished || o.state.Phase == PhaseAborted {
		o.mu.Unlock()
		return
	}
	o.state.Phase = PhasePaused
	o.state.PausedAtSeq = atSeq
	o.mu.Unlock()
	o.snapshot()
}

// Finish moves to terminal PhaseFinished. Idempotent.
func (o *Operator) Finish(reason string) {
	o.mu.Lock()
	if o.state.Phase == PhaseFinished {
		o.mu.Unlock()
		return
	}
	o.state.Phase = PhaseFinished
	o.mu.Unlock()
	o.snapshot()
}

func (o *Operator) Phase() Phase {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.state.Phase
}

func (o *Operator) PausedAtSeq() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.state.PausedAtSeq
}

func (o *Operator) snapshot() {
	o.mu.Lock()
	s := o.state
	o.mu.Unlock()
	o.persist(s)
}

// EventLite is the subset of EventEnvelope the AutoOperator needs to compose
// prompts. Keeping it local lets the auto package stay free of a server-pkg
// import.
type EventLite struct {
	Seq      int
	Kind     string
	Text     string
	ToolName string
	Origin   string
}

// ComposeDiffPrompt renders the post-`end` transcript diff the operator
// agent's next turn receives. Tool result blobs are omitted to keep the
// prompt bounded.
func ComposeDiffPrompt(events []EventLite) string {
	var b strings.Builder
	b.WriteString("Investigation agent turn ended. Recent transcript:\n\n")
	for _, e := range events {
		switch e.Kind {
		case "assistant":
			if e.Text != "" {
				b.WriteString("[agent] " + e.Text + "\n")
			}
		case "tool_use":
			b.WriteString("[agent tool] " + e.ToolName + "\n")
		case "user":
			tag := "human"
			if e.Origin == "operator" {
				tag = "you (operator)"
			}
			b.WriteString("[" + tag + "] " + e.Text + "\n")
		case "result", "end":
			// turn boundary — skip
		}
	}
	b.WriteString("\nRespond with exactly one action: send_message, request_takeover, or finish.")
	return b.String()
}

// ComposeCatchupPrompt is the resume briefing. Prefixed so the operator agent
// knows the human was driving.
func ComposeCatchupPrompt(events []EventLite) string {
	var b strings.Builder
	b.WriteString("While you were paused, the human took over. Here's the transcript span. Catch up, then continue as the operator from the latest state.\n\n")
	b.WriteString(ComposeDiffPrompt(events))
	return b.String()
}

// Wake sends a fresh diff prompt to the backend if the operator is active.
// Caller (boundary watcher) decides when to invoke.
func (o *Operator) Wake(ctx context.Context, diff []EventLite) error {
	o.mu.Lock()
	if !o.state.IsActive() {
		o.mu.Unlock()
		return nil
	}
	if o.state.WakeCount >= HardWakeCap {
		o.state.Phase = PhaseAborted
		o.mu.Unlock()
		o.snapshot()
		return ErrWakeCapExceeded
	}
	o.state.WakeCount++
	wake := o.state.WakeCount
	o.mu.Unlock()
	o.snapshot()

	prompt := ComposeDiffPrompt(diff)
	if wake > SoftWakeCap {
		prompt = "[note] You've had many turns; consider calling `finish` soon.\n\n" + prompt
	}

	if err := o.backend.Resume(ctx, prompt); err != nil {
		o.recordFailure()
		return err
	}
	o.recordSuccess()
	return nil
}

func (o *Operator) recordFailure() {
	o.mu.Lock()
	o.state.ConsecFailures++
	if o.state.ConsecFailures >= 2 {
		o.state.Phase = PhaseAborted
	}
	o.mu.Unlock()
	o.snapshot()
}

func (o *Operator) recordSuccess() {
	o.mu.Lock()
	o.state.ConsecFailures = 0
	o.mu.Unlock()
	o.snapshot()
}

// Resume composes the catch-up briefing, transitions to PhaseResumed, and
// pushes the prompt to the backend.
func (o *Operator) Resume(ctx context.Context, span []EventLite) error {
	o.mu.Lock()
	if o.state.Phase != PhasePaused {
		o.mu.Unlock()
		return nil
	}
	o.state.Phase = PhaseResumed
	o.mu.Unlock()
	o.snapshot()
	return o.backend.Resume(ctx, ComposeCatchupPrompt(span))
}
