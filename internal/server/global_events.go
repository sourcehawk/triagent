package server

import (
	"sync"
	"time"
)

// Window for replay-on-connect. Events older than this are filtered out
// when a new client subscribes. The cap is informational on top of that:
// events older than the window are dropped from the buffer regardless.
const (
	globalReplayWindow = 5 * time.Minute
	globalRingCap      = 50
)

// Envelope kinds carried on the launcher-level SSE channel. Distinct
// from the per-investigation envKind* set so a misrouted event would
// stand out.
const (
	globalKindRepoSummaryState    = "repo_summary_state"
	globalKindCodefixPRState      = "codefix_pr_state"
	globalKindWatchStatus         = "watch_status"
	globalKindSignalCreated       = "signal_created"
	globalKindItemCaptured        = "item_captured"
	globalKindIngestRunStarted    = "ingest_run_started"
	globalKindIngestRunFinished   = "ingest_run_finished"
	globalKindWikiProposalCreated = "wiki_proposal_created"
)

// GlobalEventEnvelope is the wire shape on /api/events. Mirrors
// EventEnvelope but carries launcher-wide payloads — anything that's
// not tied to a specific investigation. Future kinds (mcp connection
// reconnects, playbook approval propagation, etc) extend this struct.
type GlobalEventEnvelope struct {
	Seq       int       `json:"seq"`
	Kind      string    `json:"kind"`
	Timestamp time.Time `json:"timestamp"`

	// RepoSummary is set when Kind == "repo_summary_state".
	RepoSummary *RepoSummaryStatePayload `json:"repoSummary,omitempty"`

	// CodefixPRState is set when Kind == "codefix_pr_state".
	CodefixPRState *CodefixPRStatePayload `json:"codefixPRState,omitempty"`

	WatchStatus         *WatchStatusEvent         `json:"watchStatus,omitempty"`
	SignalCreated       *SignalCreatedEvent       `json:"signalCreated,omitempty"`
	ItemCaptured        *ItemCapturedEvent        `json:"itemCaptured,omitempty"`
	IngestRunStarted    *IngestRunStartedEvent    `json:"ingestRunStarted,omitempty"`
	IngestRunFinished   *IngestRunFinishedEvent   `json:"ingestRunFinished,omitempty"`
	WikiProposalCreated *WikiProposalCreatedEvent `json:"wikiProposalCreated,omitempty"`
}

// WikiProposalCreatedEvent fires when a propose_wiki_draft tool call lands a
// new draft on disk. The sidebar's pending-proposals list refetches on it, so
// a proposal surfaces live even when the draft was made inside a playbook
// sub-agent (whose nested tool result the transcript-card path can't see).
type WikiProposalCreatedEvent struct {
	ProposalID      string `json:"proposalID,omitempty"`
	InvestigationID string `json:"investigationID,omitempty"`
}

// IngestRunStartedEvent fires when ClaudeIngestor.Run spawns claude.
// The UI listens to flip the matching row to "running" without polling
// the disk.
type IngestRunStartedEvent struct {
	WatchID   string `json:"watchID"`
	RunID     string `json:"runID"`
	StartedAt string `json:"startedAt"`
	ItemCount int    `json:"itemCount"`
}

// IngestRunFinishedEvent fires when ClaudeIngestor.Run returns. The
// status field is "ok" / "error"; durationMs measures the spawned
// claude process's wall-clock runtime.
type IngestRunFinishedEvent struct {
	WatchID    string `json:"watchID"`
	RunID      string `json:"runID"`
	Status     string `json:"status"`
	DurationMs int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
}

// WatchStatusEvent fires on every poll outcome and on every spawner
// state change (added in milestone 6).
type WatchStatusEvent struct {
	WatchID      string `json:"watchID"`
	Status       string `json:"status"` // healthy | unhealthy | disabled
	LastPolledAt string `json:"lastPolledAt,omitempty"`
	ErrorCount   int    `json:"errorCount,omitempty"`
	Running      int    `json:"running"`
	Queued       int    `json:"queued"`
}

// SignalCreatedEvent fires whenever a new row lands in signals.jsonl.
type SignalCreatedEvent struct {
	WatchID         string `json:"watchID"`
	SignalID        string `json:"signalID"`
	Outcome         string `json:"outcome"`
	InvestigationID string `json:"investigationID,omitempty"`
	ManuallyStarted bool   `json:"manuallyStarted,omitempty"`
}

// ItemCapturedEvent fires on every item appended to items.jsonl.
type ItemCapturedEvent struct {
	WatchID  string `json:"watchID"`
	ItemID   string `json:"itemID"`
	SignalID string `json:"signalID,omitempty"`
	Filtered bool   `json:"filtered,omitempty"`
}

// CodefixPRStatePayload is the lifecycle event for a codefix
// proposal's PR. The poller emits one of these each time a tracked
// codefix PR transitions state (open → merged/closed). The frontend
// reads it off /api/events to flip CodefixProposalCard lifecycle.
type CodefixPRStatePayload struct {
	URL      string     `json:"url"`
	State    string     `json:"state"` // "open" | "merged" | "closed" | "" (unknown)
	MergedAt *time.Time `json:"mergedAt,omitempty"`
	ClosedAt *time.Time `json:"closedAt,omitempty"`
}

// RepoSummaryStatePayload is the lifecycle event for a per-repo
// generation. Phase transitions: started -> success | error. The
// frontend reads this off /api/events to update sidenav status icons
// and pop the completion / failure toast.
type RepoSummaryStatePayload struct {
	Owner       string     `json:"owner"`
	Name        string     `json:"name"`
	Phase       string     `json:"phase"` // "started" | "success" | "error"
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	GeneratedAt *time.Time `json:"generatedAt,omitempty"`
	ByteCount   int        `json:"byteCount,omitempty"`
	Kind        string     `json:"kind,omitempty"`
	Error       string     `json:"error,omitempty"`
}

// globalRing is a small replay buffer for the global SSE stream.
// Capped at 50 entries; replay() drops anything older than the
// 5-minute window. Survives reconnects within the window so a freshly
// reopened launcher tab catches up to events that fired while it was
// closed (typical case: operator hits refresh, kicks off a generation,
// tabs away, comes back when the toast lands).
type globalRing struct {
	mu     sync.Mutex
	buffer []GlobalEventEnvelope
}

func newGlobalRing() *globalRing { return &globalRing{} }

func (r *globalRing) append(env GlobalEventEnvelope) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buffer = append(r.buffer, env)
	if len(r.buffer) > globalRingCap {
		// Drop the oldest entries. Slice operation is fine; ring
		// stays small enough that copying isn't worth optimising.
		r.buffer = r.buffer[len(r.buffer)-globalRingCap:]
	}
}

// replay returns events newer than (now - globalReplayWindow), in
// arrival order. Caller is the SSE handler at subscribe time.
func (r *globalRing) replay(now time.Time) []GlobalEventEnvelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := now.Add(-globalReplayWindow)
	out := make([]GlobalEventEnvelope, 0, len(r.buffer))
	for _, env := range r.buffer {
		if env.Timestamp.Before(cutoff) {
			continue
		}
		out = append(out, env)
	}
	return out
}
