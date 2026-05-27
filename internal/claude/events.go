// Package claude wraps the `claude` CLI for streamed investigation sessions.
// The UI drives it via Session.Start and Session.Resume, which exec claude
// with --output-format stream-json and fan its stdout out as typed Events.
package claude

import (
	"encoding/json"
)

// EventKind discriminates the shape of an Event coming off the stream.
type EventKind string

const (
	EventSystem     EventKind = "system"
	EventAssistant  EventKind = "assistant"
	EventToolUse    EventKind = "tool_use"
	EventToolResult EventKind = "tool_result"
	EventResult     EventKind = "result"
	// EventUsage is the accounting-only event emitted alongside every
	// assistant message that carries a `usage` block. Used so the launcher
	// can sum tokens across every API call in a CLI invocation — the
	// `result` line's usage is just the last API call's snapshot, not the
	// invocation total, so result-only summing under-reports dramatically.
	EventUsage   EventKind = "usage"
	EventUnknown EventKind = "unknown"
	EventError   EventKind = "error" // synthesised on process errors
)

// Usage is the per-API-call token tally claude emits on assistant
// messages and on the per-invocation result line. Each component is
// reported in tokens (not characters). Pointer field on Event so a
// nil distinguishes "no usage reported" from "all zeros".
type Usage struct {
	InputTokens              int `json:"inputTokens,omitempty"`
	OutputTokens             int `json:"outputTokens,omitempty"`
	CacheCreationInputTokens int `json:"cacheCreationInputTokens,omitempty"`
	CacheReadInputTokens     int `json:"cacheReadInputTokens,omitempty"`
}

// Event is a flattened, UI-friendly view of one stream-json line.
type Event struct {
	Kind EventKind
	// MessageID is the Anthropic API message id (Message.id on the wire).
	// Stamped onto EventUsage so the stream loop can dedupe per-API-call
	// usage: Claude Code splits one assistant message into multiple
	// stream-json lines (one per content-block kind — thinking / tool_use
	// / text), and each line repeats the same `usage` block. Without the
	// id we'd sum the same API call N× per message.
	MessageID  string
	SessionID  string
	Subtype    string // e.g. "init" for system, "success"/"error_max_turns" for result
	Text       string // concatenated text from assistant content blocks
	ToolID     string // claude tool_use id; tool_result references it via tool_use_id
	ToolName   string
	ToolInput  map[string]any
	ToolResult string
	FinalText  string // populated for result events (assistant's closing message)
	// Usage is the message-level (assistant) or invocation-level (result)
	// token tally. nil when claude omitted the block. For assistant
	// messages that flatten into multiple events (text + N tool_use),
	// Usage rides on the first event only — duplicating it across the
	// split would double-count when an aggregator sums per Event.
	Usage *Usage
	// CostUSD is populated on result events only (total_cost_usd in the
	// wire format). Zero on assistant / system / tool events.
	CostUSD float64
	Err     error // non-nil only on a synthesised error event
	Raw     json.RawMessage
}

// usageDeduper keeps the first EventUsage per Anthropic message id and
// drops repeats. Claude Code's stream-json mode splits one assistant API
// message into multiple lines (one per content-block kind — thinking,
// tool_use, text) and each line carries the SAME `usage` block, so naive
// summing over-counts by a factor of N (~2× in practice). The deduper
// is per-invocation (one Session = one fresh deduper); msg ids are
// globally unique per API call so we never need to evict.
//
// Usage events without a MessageID (legacy claude builds or unrecognised
// shapes) pass through — dropping would silently under-report.
type usageDeduper struct {
	seen map[string]struct{}
}

func newUsageDeduper() *usageDeduper {
	return &usageDeduper{seen: map[string]struct{}{}}
}

// filter returns events with duplicate-msg-id EventUsage stripped. Other
// event kinds (assistant text, tool_use, tool_result, …) pass through
// untouched even when they share a message with a deduped usage event.
func (d *usageDeduper) filter(events []Event) []Event {
	out := events[:0]
	for _, ev := range events {
		if ev.Kind == EventUsage && ev.MessageID != "" {
			if _, dup := d.seen[ev.MessageID]; dup {
				continue
			}
			d.seen[ev.MessageID] = struct{}{}
		}
		out = append(out, ev)
	}
	return out
}

// parseLine turns one stream-json line into one or more Events. Claude
// Code emits message-typed lines whose `content` field is an array of
// blocks: an `assistant` line carries text + zero-or-more `tool_use`
// blocks; a synthesised `user` line carries `tool_result` blocks. We
// flatten that into separate Events per block so the UI can render each
// independently and the activity panel can correlate them by tool_use id.
func parseLine(line []byte) []Event {
	var head struct {
		Type      string `json:"type"`
		Subtype   string `json:"subtype"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		return []Event{{Kind: EventError, Err: err, Raw: append(json.RawMessage(nil), line...)}}
	}

	raw := append(json.RawMessage(nil), line...)
	base := func() Event {
		return Event{SessionID: head.SessionID, Subtype: head.Subtype, Raw: raw}
	}

	switch head.Type {
	case "system":
		ev := base()
		ev.Kind = EventSystem
		return []Event{ev}
	case "assistant":
		return parseAssistant(line, base)
	case "user":
		// Synthesised by Claude Code when it's reporting a tool result
		// back into the conversation. The content[] holds tool_result
		// blocks; flatten each to a separate event.
		if results := parseToolResults(line, base); len(results) > 0 {
			return results
		}
		ev := base()
		ev.Kind = EventUnknown
		return []Event{ev}
	case "tool_use":
		// Some claude builds emit top-level tool_use lines; keep this
		// branch for compatibility.
		ev := base()
		ev.Kind = EventToolUse
		ev.ToolID, ev.ToolName, ev.ToolInput = extractToolUseTopLevel(line)
		return []Event{ev}
	case "tool_result":
		ev := base()
		ev.Kind = EventToolResult
		ev.ToolID, ev.ToolResult = extractToolResultTopLevel(line)
		return []Event{ev}
	case "result":
		ev := base()
		ev.Kind = EventResult
		ev.FinalText, ev.Usage, ev.CostUSD = extractResultUsage(line)
		return []Event{ev}
	default:
		ev := base()
		ev.Kind = EventUnknown
		return []Event{ev}
	}
}

// parseAssistant flattens a `{"type":"assistant","message":{"content":[…]}}`
// line into an EventAssistant carrying the concatenated text plus one
// EventToolUse per tool_use block. Content blocks of unknown types are
// ignored. Both events share the session id and subtype so the renderer
// can stamp them consistently.
func parseAssistant(line []byte, base func() Event) []Event {
	var m struct {
		Message struct {
			ID      string `json:"id"`
			Content []struct {
				Type  string         `json:"type"`
				Text  string         `json:"text"`
				ID    string         `json:"id"`
				Name  string         `json:"name"`
				Input map[string]any `json:"input"`
			} `json:"content"`
			Usage *struct {
				InputTokens              int `json:"input_tokens"`
				OutputTokens             int `json:"output_tokens"`
				CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
				CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &m); err != nil {
		ev := base()
		ev.Kind = EventError
		ev.Err = err
		return []Event{ev}
	}

	var usage *Usage
	if m.Message.Usage != nil {
		usage = &Usage{
			InputTokens:              m.Message.Usage.InputTokens,
			OutputTokens:             m.Message.Usage.OutputTokens,
			CacheCreationInputTokens: m.Message.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     m.Message.Usage.CacheReadInputTokens,
		}
	}

	out := make([]Event, 0, 1+len(m.Message.Content))
	var text string
	for _, c := range m.Message.Content {
		if c.Type == "text" {
			text += c.Text
		}
	}
	if text != "" {
		ev := base()
		ev.Kind = EventAssistant
		ev.Text = text
		out = append(out, ev)
	}
	for _, c := range m.Message.Content {
		if c.Type == "tool_use" {
			ev := base()
			ev.Kind = EventToolUse
			ev.ToolID = c.ID
			ev.ToolName = c.Name
			ev.ToolInput = c.Input
			out = append(out, ev)
		}
	}
	if len(out) == 0 {
		// Empty assistant message — keep one assistant event so the UI's
		// status / sequencing logic isn't surprised by a missing slot.
		ev := base()
		ev.Kind = EventAssistant
		out = append(out, ev)
	}
	// Emit a dedicated usage event so the launcher's aggregator can sum
	// tokens across every API call in the invocation. Stamping usage on
	// out[0] (the historic approach) lost it whenever out[0] was an
	// EventToolUse — those get dropped by the server-side mapper and
	// took the usage with them. The dedicated EventUsage survives the
	// drop and is the canonical per-message accounting carrier.
	//
	// MessageID rides on the event so the stream-loop can dedupe: Claude
	// Code splits a single assistant API message into multiple stream-json
	// lines (thinking / tool_use / text) and each line repeats the same
	// `usage` block. Aggregators must sum only once per message id.
	if usage != nil {
		ev := base()
		ev.Kind = EventUsage
		ev.MessageID = m.Message.ID
		ev.Usage = usage
		out = append(out, ev)
	}
	return out
}

// parseToolResults pulls each tool_result content block out of a
// synthesised user message. Each block correlates back to its tool_use
// via tool_use_id; the rendered text is concatenated from any text
// sub-blocks (or, for legacy formats, the bare `content` string).
func parseToolResults(line []byte, base func() Event) []Event {
	var m struct {
		Message struct {
			Content []struct {
				Type      string          `json:"type"`
				ToolUseID string          `json:"tool_use_id"`
				Content   json.RawMessage `json:"content"`
				IsError   bool            `json:"is_error"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &m); err != nil {
		return nil
	}
	out := make([]Event, 0, len(m.Message.Content))
	for _, c := range m.Message.Content {
		if c.Type != "tool_result" {
			continue
		}
		ev := base()
		ev.Kind = EventToolResult
		ev.ToolID = c.ToolUseID
		ev.ToolResult = decodeToolResultContent(c.Content)
		out = append(out, ev)
	}
	return out
}

// decodeToolResultContent handles both shapes claude emits for a tool
// result body: a bare string, or an array of `{type,text}` content blocks.
func decodeToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var out string
		for _, b := range blocks {
			if b.Type == "text" {
				out += b.Text
			}
		}
		return out
	}
	return ""
}

// extractToolUseTopLevel + extractToolResultTopLevel handle the legacy
// "claude emits tool_* as top-level lines" format. Kept for compatibility.

func extractToolUseTopLevel(line []byte) (id, name string, input map[string]any) {
	var m struct {
		ID    string         `json:"id"`
		Name  string         `json:"name"`
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(line, &m); err != nil {
		return "", "", nil
	}
	return m.ID, m.Name, m.Input
}

func extractToolResultTopLevel(line []byte) (id, result string) {
	var m struct {
		ToolUseID string `json:"tool_use_id"`
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Result string `json:"result"`
	}
	if err := json.Unmarshal(line, &m); err != nil {
		return "", ""
	}
	if m.Result != "" {
		return m.ToolUseID, m.Result
	}
	var s string
	for _, c := range m.Content {
		if c.Type == "text" {
			s += c.Text
		}
	}
	return m.ToolUseID, s
}

// extractResultUsage decodes the final-line wire shape claude emits at
// the end of every CLI invocation: the closing assistant text plus the
// invocation-scoped usage tally and total_cost_usd. Returning the three
// together keeps the result-branch handler in parseLine tidy.
func extractResultUsage(line []byte) (string, *Usage, float64) {
	var m struct {
		Result        string  `json:"result"`
		TotalCostUSD  float64 `json:"total_cost_usd"`
		Usage         *struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(line, &m); err != nil {
		return "", nil, 0
	}
	var usage *Usage
	if m.Usage != nil {
		usage = &Usage{
			InputTokens:              m.Usage.InputTokens,
			OutputTokens:             m.Usage.OutputTokens,
			CacheCreationInputTokens: m.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     m.Usage.CacheReadInputTokens,
		}
	}
	return m.Result, usage, m.TotalCostUSD
}
