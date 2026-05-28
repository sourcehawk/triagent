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

	code, err := replay(actions, in, out, tr)
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
