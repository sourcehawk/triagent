// Package parallel implements the triagent-mcp `parallel` MCP server. It exposes
// one tool, `call`, that fans out a list of sub-calls to other MCP servers
// concurrently and returns the combined result. See the design spec at
// docs/superpowers/specs/2026-05-11-investigation-parallel-dispatch-design.md.
package parallel

// CallIn is the input schema for the `call` tool.
type CallIn struct {
	// Summary is a one-line operator-facing summary of the batch's intent.
	// Required; 1..120 chars (enforced in the handler, not the struct).
	Summary string `json:"summary" jsonschema:"one-line operator-facing summary of the batch's intent (1..120 chars). Rendered on the parallel_call card."`

	// Calls is the list of sub-calls. 2..8 items (enforced in the handler).
	Calls []SubCall `json:"calls" jsonschema:"list of sub-calls to dispatch in parallel; 2..8 items."`

	// MaxConcurrency caps in-flight sub-calls. Optional; 0 means default
	// (6), clamped to [1, 8].
	MaxConcurrency int `json:"max_concurrency,omitempty" jsonschema:"max in-flight sub-calls; 1..8; default 6."`
}

// SubCall describes one sub-call to dispatch.
type SubCall struct {
	// Server is the MCP server alias as written by preflight (e.g.
	// "triagent-git-alerts", "triagent-wiki"). No "mcp__" prefix.
	Server string `json:"server" jsonschema:"MCP server alias (e.g. triagent-git-alerts, triagent-wiki); no mcp__ prefix."`

	// Tool is the tool name on that server, without the "mcp__<alias>__"
	// prefix (e.g. "analyze_change").
	Tool string `json:"tool" jsonschema:"tool name on the target server, without mcp__<alias>__ prefix."`

	// Input is forwarded verbatim to the upstream tool.
	Input map[string]any `json:"input" jsonschema:"input forwarded verbatim to the upstream tool."`

	// Purpose is an optional one-liner describing what this sub-call is
	// for. 0..80 chars; surfaces on the nested activity-panel row.
	Purpose string `json:"purpose,omitempty" jsonschema:"optional one-liner describing this sub-call's purpose; 0..80 chars."`
}

// CallOut is the output schema for the `call` tool.
type CallOut struct {
	// Summary echoes the input.
	Summary string `json:"summary"`

	// DurationMs is the wall-clock duration of the batch (dispatch → last
	// result settle) in milliseconds.
	DurationMs int64 `json:"duration_ms"`

	// Results is positionally aligned with CallIn.Calls.
	Results []SubResult `json:"results"`
}

// SubResult is one sub-call's outcome.
type SubResult struct {
	OK       bool   `json:"ok"`
	Result   any    `json:"result,omitempty"`
	Error    string `json:"error,omitempty"`
	TimedOut bool   `json:"timed_out,omitempty"`
	Rejected bool   `json:"rejected,omitempty"`
}
