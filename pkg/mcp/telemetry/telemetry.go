// Package telemetry posts tool-call telemetry from triagent-mcp to whatever
// process is launching it. Each tool handler can be wrapped via Wrap();
// start/end events fire over HTTP to the URL the launcher hands us via
// env, with a trace id the launcher chooses (we don't care what it is —
// it just lets the receiver correlate events to its own concept of a
// session, investigation, request, etc.).
//
// When the TRIAGENT_MCP_TELEMETRY_URL env var isn't set (e.g. a standalone
// `triagent-mcp serve` smoke test or a third-party host that hasn't opted in),
// Wrap is a no-op that returns the original handler unchanged. None of
// the telemetry constants leak into the tool result either way.
package telemetry

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Env vars the launcher (any launcher) sets to enable telemetry. URL is
// the only required field; the others are optional context the receiver
// can use however it likes.
const (
	envURL        = "TRIAGENT_MCP_TELEMETRY_URL"
	envTraceID    = "TRIAGENT_MCP_TRACE_ID"
	envToken      = "TRIAGENT_MCP_TELEMETRY_TOKEN"
	envToolPrefix = "TRIAGENT_MCP_TELEMETRY_TOOL_PREFIX"
)

// telemetry config resolved once at process start. Held in package state
// because the wrap callsite needs it on every tool invocation and the
// values are immutable for the triagent-mcp lifetime.
var cfg *config

type config struct {
	url        string
	traceID    string
	token      string
	toolPrefix string
	client     *http.Client
}

func init() {
	url := os.Getenv(envURL)
	if url == "" {
		// No URL → telemetry disabled. Wrap will return handlers
		// unchanged. Trace id without a URL is meaningless, so we don't
		// activate on trace alone.
		return
	}
	cfg = &config{
		url:        url,
		traceID:    os.Getenv(envTraceID),
		token:      os.Getenv(envToken),
		toolPrefix: os.Getenv(envToolPrefix),
		// Short timeout: typical launchers are local. If telemetry hangs,
		// we'd rather drop the event than stall a tool call back to
		// claude.
		client: &http.Client{Timeout: 2 * time.Second},
	}
}

// toolIDKey is the context-value type used to expose the wrapping handler's
// tool id to the wrapped function. Sub-agent runners read it to attribute
// nested events to the correct parent.
type toolIDKey struct{}

// CurrentToolID returns the tool id that Wrap injected into ctx for the
// currently-running tool handler. Returns "" when called outside a Wrap'd
// handler or when telemetry is disabled.
func CurrentToolID(ctx context.Context) string {
	v, _ := ctx.Value(toolIDKey{}).(string)
	return v
}

// WithToolID is the symmetric setter to CurrentToolID. Production code
// gets a tool id on ctx via Wrap; tests and callers that compose tool
// handlers outside Wrap (e.g. unit tests asserting parent-id plumbing
// into sub-agent dispatches) construct a ctx with a known id by calling
// this. Returning ctx unchanged when id is empty keeps test setup terse.
func WithToolID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, toolIDKey{}, id)
}

// Wrap returns a tool handler that fires phase=start before the wrapped
// handler runs and phase=end after, attributing both to the same
// generated tool id. When telemetry isn't configured, returns fn
// unchanged.
//
// shortName is the bare tool name as registered with mcp.AddTool (e.g.
// "cpu_pressure"); the launcher's tool prefix env var supplies the
// `mcp__<alias>__` prefix so reported names match the wire format the
// activity panel groups by.
//
// The generated tool id is also injected into ctx via CurrentToolID so
// handlers that spawn nested work (sub-agents) can attribute child events
// back to this call.
//
// Returns mcp.ToolHandlerFor (the SDK's defined type) rather than a
// raw func so the result lines up with mcp.AddTool's parameter type.
func Wrap[In, Out any](shortName string, fn mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	if cfg == nil {
		return fn
	}
	fullName := cfg.toolPrefix + shortName
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		toolID := newToolID()
		cfg.send(toolEvent{
			Phase:    "start",
			TraceID:  cfg.traceID,
			ToolID:   toolID,
			ToolName: fullName,
			Input:    toMap(in),
		})
		ctx = context.WithValue(ctx, toolIDKey{}, toolID)
		callResult, out, callErr := fn(ctx, req, in)
		cfg.send(toolEvent{
			Phase:     "end",
			TraceID:   cfg.traceID,
			ToolID:    toolID,
			Result:    stringifyResult(callResult, out),
			ErrorText: resultError(callResult, callErr),
		})
		return callResult, out, callErr
	}
}

// SendNested posts a synthetic phase=start + phase=end pair attributed to
// a parent tool id, surfacing as a collapsed child event in the launcher's
// activity panel. Used by handlers that spawn sub-agents and want to
// attribute the sub-agent's own internal tool calls / messages back to the
// parent. Caller supplies a fresh child id (via NewToolID) so start/end
// correlate. No-op when telemetry isn't configured.
func SendNested(ev NestedEvent) {
	if cfg == nil {
		return
	}
	cfg.send(toolEvent{
		Phase:        ev.Phase,
		TraceID:      cfg.traceID,
		ToolID:       ev.ToolID,
		ToolName:     ev.ToolName,
		Input:        ev.Input,
		Result:       ev.Result,
		ErrorText:    ev.ErrorText,
		ParentToolID: ev.ParentToolID,
	})
}

// SendStatus posts a phase=status event attributed to the running
// tool's parent. Used by sub-agent runners to forward
// <<<STATUS message="...">>> markers from the spawned claude's
// stdout. The launcher renders these as italic grey lines under the
// in-flight tool card. parentToolID is whatever the caller resolved
// from CurrentToolID(ctx); message is the status text. No-op when
// telemetry isn't configured.
func SendStatus(parentToolID, message string) {
	if cfg == nil {
		return
	}
	cfg.send(toolEvent{
		Phase:        "status",
		TraceID:      cfg.traceID,
		ParentToolID: parentToolID,
		Result:       message,
	})
}

// NestedEvent is the input to SendNested. Phase is "start" or "end". One
// pair per logical sub-step; the activity panel groups them by ToolID.
type NestedEvent struct {
	Phase        string
	ParentToolID string
	ToolID       string
	ToolName     string
	Input        map[string]any
	Result       string
	ErrorText    string
}

// NewToolID returns a fresh hex tool id. Exported so callers building
// nested events (sub-agents) can mint correlated start/end ids.
func NewToolID() string { return newToolID() }

// toolEvent matches the JSON shape any telemetry-receiving host should
// decode. The trace id is whatever the launcher set in env; receivers
// use it to attribute events to their own concept of a session.
type toolEvent struct {
	Phase        string         `json:"phase"`
	TraceID      string         `json:"traceId,omitempty"`
	ToolID       string         `json:"toolId"`
	ToolName     string         `json:"toolName,omitempty"`
	Input        map[string]any `json:"input,omitempty"`
	Result       string         `json:"result,omitempty"`
	ErrorText    string         `json:"errorText,omitempty"`
	ParentToolID string         `json:"parentToolId,omitempty"`
}

func (c *config) send(ev toolEvent) {
	body, err := json.Marshal(ev)
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

// newToolID returns a short hex id used to correlate phase=start and
// phase=end events on the launcher side.
func newToolID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return "tooluid_" + hex.EncodeToString(buf)
}

// toMap reflects the typed In into a JSON-friendly map by round-tripping
// through encoding/json. The MCP SDK already decodes In from JSON, so
// this round-trip is cheap and lossless. Returns nil on error so the
// telemetry payload simply omits Input.
func toMap(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}

// stringifyResult reduces a CallToolResult down to a single string for
// the activity panel preview. Concatenates text content blocks; falls
// back to the structured Out value (re-serialised) when there are no
// text blocks. Truncated downstream by the panel as needed.
func stringifyResult(r *mcp.CallToolResult, out any) string {
	if r != nil {
		var s string
		for _, c := range r.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				s += tc.Text
			}
		}
		if s != "" {
			return s
		}
	}
	if out == nil {
		return ""
	}
	if b, err := json.Marshal(out); err == nil {
		return string(b)
	}
	return ""
}

// resultError captures the error path: a returned `error`, an error
// CallToolResult (IsError flag), or empty when the call succeeded.
func resultError(r *mcp.CallToolResult, err error) string {
	if err != nil {
		return err.Error()
	}
	if r != nil && r.IsError {
		var s string
		for _, c := range r.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				s += tc.Text
			}
		}
		return s
	}
	return ""
}
