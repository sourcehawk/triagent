//go:build e2e

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// trace appends one JSON object per line to
// <CLAUDE_STUB_TRACE_DIR>/claude-stub.trace.<role>.<pid>.jsonl. Tests parse
// this to assert what reached the agent: the full argv, the resolved
// system-prompt body, the parsed allowed-tools, the MCP config JSON, and
// every event the launcher fed over stdin.
type trace struct {
	f   *os.File
	enc *json.Encoder
}

// newTrace opens the per-role, per-pid trace file. CLAUDE_STUB_TRACE_DIR is
// the directory (under the launcher's state dir) the harness reads back via
// (*Harness).StubTrace.
func newTrace(role string) (*trace, error) {
	dir := os.Getenv("CLAUDE_STUB_TRACE_DIR")
	if dir == "" {
		return nil, fmt.Errorf("CLAUDE_STUB_TRACE_DIR is unset")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create trace dir: %w", err)
	}
	name := fmt.Sprintf("claude-stub.trace.%s.%d.jsonl", role, os.Getpid())
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return nil, fmt.Errorf("create trace file: %w", err)
	}
	return &trace{f: f, enc: json.NewEncoder(f)}, nil
}

// record writes one trace line. Encoding failures are silent: the trace is
// a test aid, not a correctness gate for the stub's replay.
func (t *trace) record(v any) {
	_ = t.enc.Encode(v)
}

func (t *trace) Close() {
	_ = t.f.Close()
}
