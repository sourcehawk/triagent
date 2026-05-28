package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// action is one line of the stub script (fixtures/stub-scripts/<test>/main.jsonl).
// The vocabulary is the stub-script-format contract:
//
//	{"action":"record_args"}
//	{"action":"emit","event":{"type":"assistant_message","text":"..."}}
//	{"action":"emit","event":{"type":"tool_call","name":"summarize","args":{...}}}
//	{"action":"emit","event":{"type":"tool_result","ok":true}}
//	{"action":"emit","event":{"type":"end"}}
//	{"action":"expect_tool_call","name":"summarize"}
//	{"action":"expect_tool_result"}
//	{"action":"wait_for_signal","name":"regenerate-released"}
//	{"action":"exit","code":0}
type action struct {
	Action string          `json:"action"`
	Event  json.RawMessage `json:"event,omitempty"`
	Name   string          `json:"name,omitempty"`
	Code   int             `json:"code,omitempty"`
}

// scriptEvent is the simplified event shape carried by an "emit" action.
// The replayer translates it to the claude stream-json wire shape the
// launcher parses (internal/claude/events.go::parseLine).
type scriptEvent struct {
	Type string         `json:"type"`
	Text string         `json:"text,omitempty"`
	Name string         `json:"name,omitempty"`
	Args map[string]any `json:"args,omitempty"`
	OK   bool           `json:"ok,omitempty"`
}

// loadScript reads and parses the JSONL script, one action per non-blank,
// non-comment line. A malformed line is a fatal authoring error — surface
// it loudly rather than silently skipping.
func loadScript(path string) ([]action, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open script %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var actions []action
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "//") {
			continue
		}
		var a action
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			return nil, fmt.Errorf("%s:%d: invalid action JSON: %w", path, line, err)
		}
		actions = append(actions, a)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read script %s: %w", path, err)
	}
	return actions, nil
}

// stubSessionID is the stream-json session id the stub stamps on every line
// so the launcher can capture it and pass --resume on the next turn.
const stubSessionID = "claude-stub-session"

// k8sServerAlias is the per-session MCP server the k8s round-trip drives. It
// mirrors preflight.MCPAliasK8s; the stub can't import internal/preflight, so
// the literal is pinned here and the e2e k8s test asserts the wire path end to
// end (a drift in the alias surfaces as a "no server" round-trip failure).
const k8sServerAlias = "triagent-k8s"

// replayDeps carries the side inputs the action loop needs beyond the script
// itself. mcpConfigPath is the --mcp-config the launcher passed; the
// expect_tool_result action uses it to spawn the real MCP server and perform
// the genuine tool round-trip. Empty disables the round-trip (the action then
// degrades to the #14 stdin-yield, keeping non-k8s flows unaffected).
type replayDeps struct {
	mcpConfigPath string
}

// replay runs the action loop. It returns the scripted exit code (0 unless
// an "exit" action sets otherwise). out is flushed by the caller.
func replay(actions []action, in *bufio.Reader, out *bufio.Writer, tr *trace, deps replayDeps) (int, error) {
	// Always open with a system/init line so the launcher captures the
	// session id, mirroring the real CLI's first stream-json line.
	if err := emitRaw(out, map[string]any{
		"type":       "system",
		"subtype":    "init",
		"session_id": stubSessionID,
	}); err != nil {
		return 0, err
	}

	// lastToolCall tracks the most recent tool_call emit so expect_tool_result
	// knows which tool (and args) to drive against the real MCP server.
	var lastToolCall *scriptEvent

	// One MCP client pool per replay; reused across every expect_tool_result so
	// a switch_context binding survives into the later list_* calls. Closed at
	// the end so no MCP child outlives the stub (keeps the subprocess registry
	// clean at test end).
	var pool *mcpPool
	if deps.mcpConfigPath != "" {
		pool = newMCPPool(deps.mcpConfigPath)
		defer pool.closeAll()
	}

	for _, a := range actions {
		switch a.Action {
		case "record_args":
			tr.record(map[string]any{"event": "record_args"})
		case "emit":
			ev, err := emitEvent(out, a.Event)
			if err != nil {
				return 0, err
			}
			if ev != nil && ev.Type == "tool_call" {
				captured := *ev
				lastToolCall = &captured
			}
		case "expect_tool_call":
			// Marks that the launcher is expected to dispatch the tool the
			// stub just emitted. With the stub acting as the MCP client, the
			// dispatch is realized by the paired expect_tool_result; this
			// action only records the expectation for the trace.
			tr.record(map[string]any{"event": "expect_tool_call", "name": a.Name})
		case "expect_tool_result":
			// The real round-trip: spawn the MCP server from the launcher's
			// per-session config and call the last-emitted tool against it
			// (against envtest, for the k8s flow). Record the result so the
			// test can assert the returned namespaces / pods. With no MCP
			// config (non-k8s flows), degrade to the #14 stdin yield.
			if pool == nil || lastToolCall == nil {
				ev := readStdinEvent(in)
				tr.record(map[string]any{"event": "stdin", "action": a.Action, "stdin": ev})
				break
			}
			res, err := pool.call(k8sServerAlias, lastToolCall.Name, lastToolCall.Args)
			if err != nil {
				tr.record(map[string]any{"event": "tool_result", "tool": lastToolCall.Name, "error": err.Error()})
				return 0, fmt.Errorf("tool round-trip %q: %w", lastToolCall.Name, err)
			}
			tr.record(map[string]any{
				"event":      "tool_result",
				"tool":       res.Tool,
				"server":     res.Server,
				"isError":    res.IsError,
				"text":       res.Text,
				"structured": res.Structured,
			})
		case "wait_for_signal":
			waitForSignal(a.Name)
			tr.record(map[string]any{"event": "signal", "name": a.Name})
		case "exit":
			return a.Code, nil
		default:
			return 0, fmt.Errorf("unknown script action %q", a.Action)
		}
	}
	return 0, nil
}

// emitEvent translates one simplified script event to its stream-json wire
// shape and writes it. "end" emits the closing result line. It returns the
// parsed event so the caller can track the most recent tool_call for the
// expect_tool_result round-trip.
func emitEvent(out *bufio.Writer, raw json.RawMessage) (*scriptEvent, error) {
	var ev scriptEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return nil, fmt.Errorf("invalid emit event: %w", err)
	}
	return &ev, emitParsed(out, &ev)
}

// emitParsed writes the wire shape for an already-parsed script event.
func emitParsed(out *bufio.Writer, ev *scriptEvent) error {
	switch ev.Type {
	case "assistant_message":
		return emitRaw(out, map[string]any{
			"type":       "assistant",
			"session_id": stubSessionID,
			"message": map[string]any{
				"id":      "msg-" + nextID(),
				"content": []any{map[string]any{"type": "text", "text": ev.Text}},
			},
		})
	case "tool_call":
		return emitRaw(out, map[string]any{
			"type":       "assistant",
			"session_id": stubSessionID,
			"message": map[string]any{
				"id": "msg-" + nextID(),
				"content": []any{map[string]any{
					"type":  "tool_use",
					"id":    "toolu-" + nextID(),
					"name":  ev.Name,
					"input": ev.Args,
				}},
			},
		})
	case "tool_result":
		return emitRaw(out, map[string]any{
			"type":       "user",
			"session_id": stubSessionID,
			"message": map[string]any{
				"content": []any{map[string]any{
					"type":        "tool_result",
					"tool_use_id": "toolu-" + lastID(),
					"content":     "ok",
					"is_error":    !ev.OK,
				}},
			},
		})
	case "end":
		return emitRaw(out, map[string]any{
			"type":       "result",
			"subtype":    "success",
			"session_id": stubSessionID,
			"result":     "",
		})
	default:
		return fmt.Errorf("unknown emit event type %q", ev.Type)
	}
}

// emitRaw writes one stream-json line (JSON object + newline).
func emitRaw(out *bufio.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := out.Write(b); err != nil {
		return err
	}
	if err := out.WriteByte('\n'); err != nil {
		return err
	}
	return out.Flush()
}

// readStdinEvent reads one line from stdin, returning "" at EOF. The
// launcher feeds the prompt as a single string; for the main flow stdin
// EOFs after that.
func readStdinEvent(in *bufio.Reader) string {
	line, err := in.ReadString('\n')
	line = strings.TrimRight(line, "\n")
	if err != nil && line == "" {
		return ""
	}
	return line
}

// waitForSignal blocks until the harness writes
// <state_dir>/signals/<name>. The signals dir is conveyed via
// CLAUDE_STUB_SIGNAL_DIR. Polls every 25ms; an unset dir means signals are
// disabled and the wait returns immediately so a misconfigured run fails
// loudly downstream rather than hanging forever.
func waitForSignal(name string) {
	dir := os.Getenv("CLAUDE_STUB_SIGNAL_DIR")
	if dir == "" || name == "" {
		return
	}
	path := filepath.Join(dir, name)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// id counters give emitted messages/tool_uses unique ids within one
// invocation. nextID/lastID let a tool_result reference the tool_call that
// preceded it.
var idCounter int

func nextID() string {
	idCounter++
	return fmt.Sprintf("%d", idCounter)
}

func lastID() string {
	return fmt.Sprintf("%d", idCounter)
}
