package server

import (
	"time"

	"github.com/sourcehawk/triagent/internal/claude"
)

// EventEnvelope is the JSON shape we ship over SSE for one transcript
// event. The Kind field discriminates the optional fields. Seq is a
// monotonic per-investigation counter that doubles as the SSE message id,
// so a reconnecting client can resume via Last-Event-ID without dupes.
type EventEnvelope struct {
	Seq       int       `json:"seq"`
	Kind      string    `json:"kind"`
	Timestamp time.Time `json:"timestamp"`

	// Per-kind optional fields. JSON-omitempty so the wire is compact.
	SessionID    string         `json:"sessionId,omitempty"`
	Subtype      string         `json:"subtype,omitempty"`
	Text         string         `json:"text,omitempty"`
	ToolID       string         `json:"toolId,omitempty"`
	ToolName     string         `json:"toolName,omitempty"`
	ToolInput    map[string]any `json:"toolInput,omitempty"`
	ParentToolID string         `json:"parentToolId,omitempty"`
	Origin       string         `json:"origin,omitempty"`

	// Set on push_state envelopes only. Carries the new state of an
	// async session push (started, succeeded, failed). The frontend
	// uses these to update the SessionView and the toast.
	PushState *PushStatePayload `json:"pushState,omitempty"`

	// Set on rehydrate_state envelopes only. Carries the phase of the
	// rehydrate orchestration (started / succeeded / failed) so the
	// frontend can render a banner without polling.
	RehydrateState *RehydrateStatePayload `json:"rehydrateState,omitempty"`

	// Set on auto_mode_state envelopes only. Carries the phase of the
	// auto-operator (started / paused / resumed / finished / aborted) and
	// (when Warning is set) an advisory-only annotation.
	AutoMode *AutoModePayload `json:"autoMode,omitempty"`

	// Usage is the message-level (assistant) or invocation-level (result)
	// token tally claude reports on the wire. nil for synthetic envelopes
	// and for claude events that omit the block. Frontend sums Usage on
	// result envelopes to compute the session total.
	Usage *claude.Usage `json:"usage,omitempty"`
	// CostUSD rides on result envelopes only. Zero elsewhere. Frontend
	// sums across result envelopes to compute the session cost.
	CostUSD float64 `json:"costUsd,omitempty"`
}

// PushStatePayload rides on a push_state envelope. Phase is always set;
// the rest depend on the phase. "pushing" carries StartedAt; "success"
// carries URL/Branch/Base; "error" carries Error.
type PushStatePayload struct {
	Phase     string     `json:"phase"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	URL       string     `json:"url,omitempty"`
	Branch    string     `json:"branch,omitempty"`
	Base      string     `json:"base,omitempty"`
	Error     string     `json:"error,omitempty"`
}

// RehydrateStatePayload rides on a rehydrate_state envelope. Phase is
// always set ("started", "succeeded", "failed"). Error is set only for
// "failed". Degraded carries human-readable downgrades the orchestrator
// applied (e.g. "prom" when the prometheus port-forward couldn't come
// up); empty for fully-successful rehydrates.
type RehydrateStatePayload struct {
	Phase    string   `json:"phase"`
	Error    string   `json:"error,omitempty"`
	Degraded []string `json:"degraded,omitempty"`
}

// AutoModePayload rides on an auto_mode_state envelope.
type AutoModePayload struct {
	Phase       string `json:"phase"` // started | paused | resumed | finished | aborted
	Reason      string `json:"reason,omitempty"`
	TakenOverBy string `json:"takenOverBy,omitempty"` // "human" | "operator"
	Error       string `json:"error,omitempty"`       // populated on aborted phase, or when Warning is set, to describe the issue.
	Warning     bool   `json:"warning,omitempty"`     // true means the envelope is advisory only and the phase did not change.
}

// Envelope kinds. The "user" and "end" kinds are synthesised by the
// server (they don't come from claude); the rest mirror claude.Event.
const (
	envKindSystem     = "system"
	envKindAssistant  = "assistant"
	envKindToolUse    = "tool_use"
	envKindToolResult = "tool_result"
	envKindToolStatus = "tool_status" // synthetic: a sub-agent emitted a STATUS marker
	envKindResult     = "result"
	envKindError      = "error"
	envKindUser       = "user"       // synthetic: a follow-up prompt the operator submitted
	envKindEnd        = "end"        // synthetic: claude exited; transcript is now idle
	envKindPushState      = "push_state"      // synthetic: an async push transitioned phase
	envKindLabel          = "label"           // synthetic: Investigation.Label was changed
	envKindRehydrateState = "rehydrate_state" // synthetic: rehydrate orchestration phase changed
	envKindAutoModeState  = "auto_mode_state" // synthetic: auto-operator phase transition (started/paused/resumed/finished/aborted)
	envKindUsage          = "usage"           // accounting-only: per-API-call token tally for the running totals
)

// fromClaudeEvent converts a claude.Event into an EventEnvelope. Returns
// (env, true) if the event has anything worth sending, (zero, false) for
// silent events (Unknown, empty assistant text, tool_*).
//
// Tool calls are intentionally dropped here. Claude's stream-json output
// doesn't reliably emit tool_use / tool_result for every call (they live
// inside assistant/user content blocks and the wire shape varies across
// versions), so we treat the stream as advisory for tool activity and
// rely on triagent-mcp's POSTed telemetry as the source of truth instead. That
// telemetry path lands in publishToolEvent below.
func fromClaudeEvent(e claude.Event) (EventEnvelope, bool) {
	env := EventEnvelope{}
	switch e.Kind {
	case claude.EventSystem:
		env.Kind = envKindSystem
		env.Subtype = e.Subtype
		env.SessionID = e.SessionID
	case claude.EventAssistant:
		if e.Text == "" {
			// Empty-text assistants (the synthetic fallback for messages
			// that were pure tool_use) carry no UI payload. Usage now flows
			// through a dedicated EventUsage event in the same parseLine
			// slice, so dropping this here loses nothing.
			return EventEnvelope{}, false
		}
		env.Kind = envKindAssistant
		env.Text = e.Text
		env.Usage = e.Usage
	case claude.EventResult:
		env.Kind = envKindResult
		env.Subtype = e.Subtype
		env.Text = e.FinalText
		// e.Usage on a result line is the LAST API call's snapshot, not the
		// invocation total — claude reports it that way. We deliberately
		// drop it here and rely on the per-call EventUsage events for token
		// totals. CostUSD is the invocation roll-up and still flows.
		env.CostUSD = e.CostUSD
	case claude.EventUsage:
		env.Kind = envKindUsage
		env.Usage = e.Usage
	case claude.EventError:
		env.Kind = envKindError
		if e.Err != nil {
			env.Text = e.Err.Error()
		}
	case claude.EventToolUse, claude.EventToolResult, claude.EventUnknown:
		return EventEnvelope{}, false
	default:
		return EventEnvelope{}, false
	}
	return env, true
}
