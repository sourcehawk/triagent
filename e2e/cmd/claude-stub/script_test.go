//go:build e2e

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadScript parses the JSONL action vocabulary, skipping blank lines and
// // comments, and surfaces a malformed line as an error rather than
// silently dropping it.
func TestLoadScript(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.jsonl")
	body := strings.Join([]string{
		`// a comment`,
		``,
		`{"action":"record_args"}`,
		`{"action":"emit","event":{"type":"assistant_message","text":"hi"}}`,
		`{"action":"exit","code":3}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	actions, err := loadScript(path)
	if err != nil {
		t.Fatalf("loadScript: %v", err)
	}
	if len(actions) != 3 {
		t.Fatalf("got %d actions, want 3: %+v", len(actions), actions)
	}
	if actions[0].Action != "record_args" {
		t.Errorf("action[0] = %q, want record_args", actions[0].Action)
	}
	if actions[2].Action != "exit" || actions[2].Code != 3 {
		t.Errorf("action[2] = %+v, want exit code 3", actions[2])
	}
}

func TestLoadScript_MalformedLineErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.jsonl")
	if err := os.WriteFile(path, []byte("{not json}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadScript(path); err == nil {
		t.Fatal("expected error on malformed action line, got nil")
	}
}

// replay translates the simplified emit vocabulary to claude stream-json and
// returns the scripted exit code. The launcher's parser keys off the wire
// "type" field, so the translation must produce assistant/tool_use/result
// shapes it recognises.
func TestReplay_TranslatesEmitToStreamJSON(t *testing.T) {
	actions := []action{
		{Action: "emit", Event: json.RawMessage(`{"type":"assistant_message","text":"working on it"}`)},
		{Action: "emit", Event: json.RawMessage(`{"type":"tool_call","name":"summarize","args":{"id":"x"}}`)},
		{Action: "emit", Event: json.RawMessage(`{"type":"end"}`)},
		{Action: "exit", Code: 0},
	}

	tr := &trace{f: os.NewFile(0, ""), enc: json.NewEncoder(&bytes.Buffer{})}
	var outBuf bytes.Buffer
	out := bufio.NewWriter(&outBuf)
	in := bufio.NewReader(strings.NewReader(""))

	code, err := replay(actions, in, out, tr, replayDeps{})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if err := out.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	lines := splitJSONL(t, outBuf.String())
	// system/init first, then assistant text, tool_use, result.
	if got := lines[0]["type"]; got != "system" {
		t.Errorf("line0 type = %v, want system", got)
	}
	if got := lines[1]["type"]; got != "assistant" {
		t.Errorf("line1 type = %v, want assistant", got)
	}
	if got := lines[2]["type"]; got != "assistant" {
		t.Errorf("line2 type = %v, want assistant (tool_use)", got)
	}
	if got := lines[len(lines)-1]["type"]; got != "result" {
		t.Errorf("last line type = %v, want result", got)
	}
}

// An "assistant_message" emit event carrying a usage block must surface
// that block on the stream-json assistant line under message.usage with
// claude's snake_case token field names. The launcher folds those per-call
// tallies into investigation.usage via its dedicated EventUsage carrier, so
// a test that wants a non-zero token readout drives it from the assistant
// turn — not the result line, whose usage the launcher deliberately drops
// for token totals.
func TestReplay_AssistantMessageCarriesUsage(t *testing.T) {
	actions := []action{
		{Action: "emit", Event: json.RawMessage(`{"type":"assistant_message","text":"working","usage":{"input_tokens":12,"output_tokens":34,"cache_creation_input_tokens":56,"cache_read_input_tokens":78}}`)},
		{Action: "exit", Code: 0},
	}

	tr := &trace{f: os.NewFile(0, ""), enc: json.NewEncoder(&bytes.Buffer{})}
	var outBuf bytes.Buffer
	out := bufio.NewWriter(&outBuf)
	in := bufio.NewReader(strings.NewReader(""))

	if _, err := replay(actions, in, out, tr, replayDeps{}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if err := out.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	lines := splitJSONL(t, outBuf.String())
	// system/init, then the assistant line with usage.
	assistant := lines[1]
	msg, ok := assistant["message"].(map[string]any)
	if !ok {
		t.Fatalf("assistant line missing message object: %v", assistant)
	}
	usage, ok := msg["usage"].(map[string]any)
	if !ok {
		t.Fatalf("assistant message missing usage block: %v", msg)
	}
	if got := usage["input_tokens"]; got != float64(12) {
		t.Errorf("input_tokens = %v, want 12", got)
	}
	if got := usage["output_tokens"]; got != float64(34) {
		t.Errorf("output_tokens = %v, want 34", got)
	}
	if got := usage["cache_creation_input_tokens"]; got != float64(56) {
		t.Errorf("cache_creation_input_tokens = %v, want 56", got)
	}
	if got := usage["cache_read_input_tokens"]; got != float64(78) {
		t.Errorf("cache_read_input_tokens = %v, want 78", got)
	}
}

// An "assistant_message" without a usage block must NOT emit a usage object —
// a stray empty/zero usage would make the launcher fold a phantom zero-token
// EventUsage. Omission means "no usage reported"; the launcher distinguishes
// that from all-zeros.
func TestReplay_AssistantMessageWithoutUsageOmitsBlock(t *testing.T) {
	actions := []action{
		{Action: "emit", Event: json.RawMessage(`{"type":"assistant_message","text":"hi"}`)},
		{Action: "exit", Code: 0},
	}
	tr := &trace{f: os.NewFile(0, ""), enc: json.NewEncoder(&bytes.Buffer{})}
	var outBuf bytes.Buffer
	out := bufio.NewWriter(&outBuf)
	in := bufio.NewReader(strings.NewReader(""))

	if _, err := replay(actions, in, out, tr, replayDeps{}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if err := out.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	lines := splitJSONL(t, outBuf.String())
	msg, _ := lines[1]["message"].(map[string]any)
	if _, present := msg["usage"]; present {
		t.Errorf("assistant line carried a usage block when none was scripted: %v", msg)
	}
}

// An "end" emit event carrying a cost (and optional usage) must surface
// total_cost_usd on the closing result line — the launcher's only source for
// the session's cost number. The usage on the result line rides through too
// so a session resurrected from disk (which replays the result envelope as a
// token backstop) shows non-zero tokens even if no EventUsage was persisted.
func TestReplay_EndCarriesCostAndUsage(t *testing.T) {
	actions := []action{
		{Action: "emit", Event: json.RawMessage(`{"type":"end","cost":0.1234,"usage":{"input_tokens":100,"output_tokens":200}}`)},
		{Action: "exit", Code: 0},
	}
	tr := &trace{f: os.NewFile(0, ""), enc: json.NewEncoder(&bytes.Buffer{})}
	var outBuf bytes.Buffer
	out := bufio.NewWriter(&outBuf)
	in := bufio.NewReader(strings.NewReader(""))

	if _, err := replay(actions, in, out, tr, replayDeps{}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if err := out.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	lines := splitJSONL(t, outBuf.String())
	result := lines[len(lines)-1]
	if got := result["type"]; got != "result" {
		t.Fatalf("last line type = %v, want result", got)
	}
	if got := result["total_cost_usd"]; got != 0.1234 {
		t.Errorf("total_cost_usd = %v, want 0.1234", got)
	}
	usage, ok := result["usage"].(map[string]any)
	if !ok {
		t.Fatalf("result line missing usage block: %v", result)
	}
	if got := usage["input_tokens"]; got != float64(100) {
		t.Errorf("result input_tokens = %v, want 100", got)
	}
	if got := usage["output_tokens"]; got != float64(200) {
		t.Errorf("result output_tokens = %v, want 200", got)
	}
}

// An "end" with neither cost nor usage must emit a bare result line with no
// total_cost_usd and no usage block, so the existing zero-cost flows stay
// untouched.
func TestReplay_EndWithoutUsageIsBare(t *testing.T) {
	actions := []action{
		{Action: "emit", Event: json.RawMessage(`{"type":"end"}`)},
		{Action: "exit", Code: 0},
	}
	tr := &trace{f: os.NewFile(0, ""), enc: json.NewEncoder(&bytes.Buffer{})}
	var outBuf bytes.Buffer
	out := bufio.NewWriter(&outBuf)
	in := bufio.NewReader(strings.NewReader(""))

	if _, err := replay(actions, in, out, tr, replayDeps{}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if err := out.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	lines := splitJSONL(t, outBuf.String())
	result := lines[len(lines)-1]
	if _, present := result["total_cost_usd"]; present {
		t.Errorf("bare end emitted total_cost_usd: %v", result)
	}
	if _, present := result["usage"]; present {
		t.Errorf("bare end emitted a usage block: %v", result)
	}
}

func splitJSONL(t *testing.T, s string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(s), "\n") {
		if raw == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("invalid stream-json line %q: %v", raw, err)
		}
		out = append(out, m)
	}
	return out
}
