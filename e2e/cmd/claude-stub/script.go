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

	// proposal-action fields. Input is the tool_use args; Result is the
	// end-phase tool result body (the proposal payload JSON the frontend
	// renders). Gh, when set, is a gh argv the proposal's backing MCP
	// tool would have shelled out to (create_github_issue → gh issue
	// create); the stub runs it so the gh-stub records the invocation.
	Input  json.RawMessage `json:"input,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Gh     []string        `json:"gh,omitempty"`
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

// replay runs the action loop with no telemetry poster — proposal actions
// degrade to stream-only. Kept as the simple entry point for unit tests
// that never configure telemetry.
func replay(actions []action, in *bufio.Reader, out *bufio.Writer, tr *trace) (int, error) {
	return replayWith(actions, in, out, tr, nil)
}

// replayWith runs the action loop. It returns the scripted exit code (0
// unless an "exit" action sets otherwise). out is flushed by the caller.
// When p is non-nil, "proposal" actions POST a start/end tool-event
// round-trip through it (the real path proposals reach the transcript);
// when p is nil they emit only the stream tool_use line.
func replayWith(actions []action, in *bufio.Reader, out *bufio.Writer, tr *trace, p *poster) (int, error) {
	// Always open with a system/init line so the launcher captures the
	// session id, mirroring the real CLI's first stream-json line.
	if err := emitRaw(out, map[string]any{
		"type":       "system",
		"subtype":    "init",
		"session_id": stubSessionID,
	}); err != nil {
		return 0, err
	}

	for _, a := range actions {
		switch a.Action {
		case "record_args":
			tr.record(map[string]any{"event": "record_args"})
		case "emit":
			if err := emitEvent(out, a.Event); err != nil {
				return 0, err
			}
		case "proposal":
			if err := emitProposal(out, tr, p, a); err != nil {
				return 0, err
			}
		case "expect_tool_call", "expect_tool_result":
			// Block until the launcher feeds the next stdin event. In this
			// suite stdin carries only the opening prompt (one read, then
			// EOF); the real MCP round-trip wiring lands with #17. Record
			// whatever arrived (or EOF) so the trace shows the wait resolved.
			ev := readStdinEvent(in)
			tr.record(map[string]any{"event": "stdin", "action": a.Action, "stdin": ev})
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

// emitProposal models one propose-* MCP tool call end to end. It POSTs a
// start/end tool-event round-trip through the poster (the only path
// proposals reach the launcher's transcript), runs any gh argv the
// backing tool would have shelled out to, emits the stream tool_use line
// for fidelity, and records the round-trip in the trace. With a nil
// poster (telemetry off) it is stream-only.
func emitProposal(out *bufio.Writer, tr *trace, p *poster, a action) error {
	if a.Name == "" {
		return fmt.Errorf("proposal action requires a tool name")
	}
	result := ""
	if len(a.Result) > 0 {
		result = string(a.Result)
	}
	if err := runGh(a.Gh); err != nil {
		return err
	}
	toolID := ""
	if p != nil {
		id, err := p.roundTrip(a.Name, a.Input, result)
		if err != nil {
			return err
		}
		toolID = id
	}
	tr.record(map[string]any{"event": "proposal", "toolName": a.Name, "toolId": toolID})

	// Stream-json tool_use line, mirroring what a real assistant turn that
	// invoked the tool would emit. The launcher drops it for transcript
	// purposes (the telemetry POST above is canonical), but emitting it
	// keeps the stub's stdout faithful to the wire.
	var input map[string]any
	if len(a.Input) > 0 {
		_ = json.Unmarshal(a.Input, &input)
	}
	return emitRaw(out, map[string]any{
		"type":       "assistant",
		"session_id": stubSessionID,
		"message": map[string]any{
			"id": "msg-" + nextID(),
			"content": []any{map[string]any{
				"type":  "tool_use",
				"id":    "toolu-" + nextID(),
				"name":  a.Name,
				"input": input,
			}},
		},
	})
}

// emitEvent translates one simplified script event to its stream-json wire
// shape and writes it. "end" emits the closing result line.
func emitEvent(out *bufio.Writer, raw json.RawMessage) error {
	var ev scriptEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return fmt.Errorf("invalid emit event: %w", err)
	}
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
