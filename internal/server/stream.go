package server

import (
	"sync"
	"time"

	"github.com/sourcehawk/triagent/internal/claude"
)

// StreamEnvelope is the wire shape on the multiplex /api/stream SSE.
// One shape carries every kind of event the launcher emits; the
// scope-id fields (InvestigationID, EditorSessionID) discriminate
// which subsystem an envelope belongs to. Both empty means a
// launcher-wide event (today only repo_summary_state).
//
// FanSeq is the per-tab monotonic fan-out sequence assigned by
// Manager.PublishStream and written into the SSE id: line for
// EventSource Last-Event-ID resumption. Distinct from Seq, which is
// the per-scope sequence (per-investigation, per-editor, per-global)
// the producer subsystems already maintain.
type StreamEnvelope struct {
	// Per-tab fan-out sequence — written into SSE id:, used for
	// Last-Event-ID resume.
	FanSeq int `json:"-"`

	// Per-scope sequence preserved from the originating subsystem.
	// Frontend uses it (with the scope id) to dedupe between the
	// /transcript snapshot and live multiplex envelopes.
	Seq       int       `json:"seq"`
	Kind      string    `json:"kind"`
	Timestamp time.Time `json:"timestamp"`

	// Scope id. Exactly one is non-empty; both empty = launcher-wide.
	InvestigationID string `json:"investigationId,omitempty"`
	EditorSessionID string `json:"editorSessionId,omitempty"`

	// Per-kind payload — superset of EventEnvelope and
	// GlobalEventEnvelope today.
	SessionID      string                   `json:"sessionId,omitempty"`
	Subtype        string                   `json:"subtype,omitempty"`
	Text           string                   `json:"text,omitempty"`
	ToolID         string                   `json:"toolId,omitempty"`
	ToolName       string                   `json:"toolName,omitempty"`
	ToolInput      map[string]any           `json:"toolInput,omitempty"`
	ParentToolID   string                   `json:"parentToolId,omitempty"`
	PushState      *PushStatePayload        `json:"pushState,omitempty"`
	RehydrateState *RehydrateStatePayload   `json:"rehydrateState,omitempty"`
	RepoSummary    *RepoSummaryStatePayload `json:"repoSummary,omitempty"`
	CodefixPRState *CodefixPRStatePayload   `json:"codefixPRState,omitempty"`
	WatchStatus       *WatchStatusEvent        `json:"watchStatus,omitempty"`
	SignalCreated     *SignalCreatedEvent      `json:"signalCreated,omitempty"`
	ItemCaptured      *ItemCapturedEvent       `json:"itemCaptured,omitempty"`
	IngestRunStarted  *IngestRunStartedEvent   `json:"ingestRunStarted,omitempty"`
	IngestRunFinished *IngestRunFinishedEvent  `json:"ingestRunFinished,omitempty"`
	// Usage rides on assistant + result envelopes (claude per-message /
	// per-CLI-invocation token tally). Subscribers that fold these into
	// running session totals read off result envelopes only.
	Usage *claude.Usage `json:"usage,omitempty"`
	// CostUSD rides on result envelopes only. See Usage above.
	CostUSD float64 `json:"costUsd,omitempty"`
}

// streamRing is a fixed-capacity ring buffer of StreamEnvelopes used
// for Last-Event-ID replay on EventSource reconnect. Capacity scales
// with expected event volume during a brief disconnect — 256 entries
// covers seconds-to-minutes of normal activity.
type streamRing struct {
	mu       sync.Mutex
	capacity int
	entries  []StreamEnvelope
}

func newStreamRing(capacity int) *streamRing {
	return &streamRing{capacity: capacity, entries: make([]StreamEnvelope, 0, capacity)}
}

// append records env in the ring, evicting the oldest entry when at
// capacity. O(N) in capacity; capacity is small.
func (r *streamRing) append(env StreamEnvelope) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.entries) == r.capacity {
		copy(r.entries, r.entries[1:])
		r.entries = r.entries[:len(r.entries)-1]
	}
	r.entries = append(r.entries, env)
}

// replay returns every entry whose FanSeq > lastEventID, in order.
// Caller-side: if lastEventID is older than the ring's earliest entry,
// the caller still gets everything we have — the consumer is expected
// to fall back to /transcript REST for full backlog beyond the ring.
func (r *streamRing) replay(lastEventID int) []StreamEnvelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]StreamEnvelope, 0, len(r.entries))
	for _, e := range r.entries {
		if e.FanSeq > lastEventID {
			out = append(out, e)
		}
	}
	return out
}
