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
	"time"
)

// wait_for_signal must block the replay until the harness writes
// <signal_dir>/<name>, and resume once it appears. This gates the
// single-flight regenerate assertion (Flow 5): the stub holds its "end"
// until the test releases the signal after observing the in-flight state.
func TestReplay_WaitForSignalBlocksUntilFileAppears(t *testing.T) {
	signalDir := t.TempDir()
	t.Setenv("CLAUDE_STUB_SIGNAL_DIR", signalDir)
	signalPath := filepath.Join(signalDir, "released")

	actions := []action{
		{Action: "wait_for_signal", Name: "released"},
		{Action: "emit", Event: json.RawMessage(`{"type":"end"}`)},
		{Action: "exit", Code: 0},
	}

	tr := &trace{f: os.NewFile(0, ""), enc: json.NewEncoder(&bytes.Buffer{})}
	var outBuf bytes.Buffer
	out := bufio.NewWriter(&outBuf)
	in := bufio.NewReader(strings.NewReader(""))

	done := make(chan int, 1)
	go func() {
		code, err := replay(actions, in, out, tr, replayDeps{})
		if err != nil {
			t.Errorf("replay: %v", err)
		}
		done <- code
	}()

	// The replay must NOT have finished while the signal file is absent.
	select {
	case <-done:
		t.Fatal("replay completed before the signal file was written")
	case <-time.After(100 * time.Millisecond):
	}

	if err := os.WriteFile(signalPath, []byte("go"), 0o600); err != nil {
		t.Fatal(err)
	}

	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("replay did not resume after the signal file was written")
	}
}

// expect_tool_call records the expectation for the trace. expect_tool_result,
// when no MCP config is wired (the non-k8s degrade path), yields the loop by
// reading one stdin line and records the wait so a test can confirm the slot
// ran. The k8s flow's real round-trip is exercised end to end in
// investigation_k8s_test.go, where an MCP config is present.
func TestReplay_ExpectActionsRecordTrace(t *testing.T) {
	actions := []action{
		{Action: "expect_tool_call", Name: "list_namespaces"},
		{Action: "expect_tool_result"},
		{Action: "exit", Code: 0},
	}

	var traceBuf bytes.Buffer
	tr := &trace{f: os.NewFile(0, ""), enc: json.NewEncoder(&traceBuf)}
	var outBuf bytes.Buffer
	out := bufio.NewWriter(&outBuf)
	in := bufio.NewReader(strings.NewReader("")) // immediate EOF

	if _, err := replay(actions, in, out, tr, replayDeps{}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	got := traceBuf.String()
	if !strings.Contains(got, `"event":"expect_tool_call"`) {
		t.Errorf("trace did not record the expect_tool_call: %s", got)
	}
	if !strings.Contains(got, `"event":"stdin"`) {
		t.Errorf("trace did not record the expect_tool_result stdin yield: %s", got)
	}
}
