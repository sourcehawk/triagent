//go:build e2e

// Flow 2b — investigation with a real k8s tool round-trip.
//
// This is the only e2e flow that exercises the full agent → launcher →
// triagent-mcp --kind=k8s → apiserver wire path: the claude-stub acts as the
// MCP client, drives switch_context / list_namespaces / list_resources against
// the launcher's per-session MCP config (backed by an envtest apiserver the
// harness booted and seeded), and records each result. The test asserts the
// round-trips completed, the returned namespaces / pods match the fixture, the
// transcript recorded the tool calls in order with the right args, and no MCP
// child outlived the stub.
package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sourcehawk/triagent/e2e/harness"
)

func TestInvestigationK8s_RealMCPRoundTrip(t *testing.T) {
	if reason := harness.EnvtestUnavailable(); reason != "" {
		t.Skipf("envtest assets unavailable: %s", reason)
	}

	h := harness.Launch(t, harness.Options{
		Profile:     "minimal-with-k8s",
		K8s:         true,
		K8sFixtures: "with-namespaces-and-pods",
		StubScript:  "k8s-roundtrip",
	})

	id := createInvestigation(t, h)
	startSession(t, h, id)
	waitForEndPolling(t, h, id, 60*time.Second)

	// --- the two round-trips completed and returned the expected state ------
	trace := h.StubTrace(t, "main")
	results := byTool(trace.ToolResults)

	nsResult, ok := results["list_namespaces"]
	if !ok {
		t.Fatalf("no list_namespaces round-trip recorded (results: %+v)", trace.ToolResults)
	}
	if nsResult.IsError {
		t.Fatalf("list_namespaces round-trip errored: %s", nsResult.Text)
	}
	names := namespaceNames(t, nsResult.Structured)
	for _, want := range []string{"team-a", "team-b"} {
		if !contains(names, want) {
			t.Errorf("list_namespaces missing %q; got %v", want, names)
		}
	}

	podResult, ok := results["list_resources"]
	if !ok {
		t.Fatalf("no list_resources round-trip recorded (results: %+v)", trace.ToolResults)
	}
	if podResult.IsError {
		t.Fatalf("list_resources round-trip errored: %s", podResult.Text)
	}
	pods := resourceNames(t, podResult.Structured)
	for _, want := range []string{"api-1", "api-2"} {
		if !contains(pods, want) {
			t.Errorf("list_resources(team-a) missing pod %q; got %v", want, pods)
		}
	}
	if contains(pods, "worker-1") {
		t.Errorf("list_resources(team-a) leaked team-b pod worker-1; got %v", pods)
	}

	// --- the launcher transcript recorded the calls in order with args ------
	// The launcher records tool names in the wire-qualified form
	// mcp__<server>__<tool> (the strings the agent's allowed-tools globs and
	// the activity panel key off).
	calls := toolCallsInOrder(t, h, id)
	const k8sPrefix = "mcp__triagent-k8s__"
	wantOrder := []string{k8sPrefix + "switch_context", k8sPrefix + "list_namespaces", k8sPrefix + "list_resources"}
	if len(calls) < len(wantOrder) {
		t.Fatalf("transcript has %d tool calls, want at least %d: %+v", len(calls), len(wantOrder), calls)
	}
	for i, want := range wantOrder {
		if calls[i].name != want {
			t.Errorf("tool call[%d] = %q, want %q (order: %+v)", i, calls[i].name, want, calls)
		}
	}
	// The namespace arg in particular must reach the k8s MCP.
	if got := calls[2].input["namespace"]; got != "team-a" {
		t.Errorf("list_resources namespace arg = %v, want team-a", got)
	}

	// --- subprocess registry is clean: launcher reports no live k8s child ---
	assertNoLiveK8sChild(t)
}

// startSession kicks off the claude turn for the investigation.
func startSession(t *testing.T, h *harness.Harness, id string) {
	t.Helper()
	status, raw := h.Client.PostJSON(t, "/api/investigations/"+id+"/start", map[string]any{})
	if status != 202 {
		t.Fatalf("POST start status %d: %s", status, raw)
	}
}

// waitForEndPolling polls the transcript REST endpoint until an "end" envelope
// appears or timeout. The end envelope is the launcher's signal that claude
// exited and the turn drained — which only happens after all three round-trips
// completed, since the stub emits end last. Distinct from the SSE-stream
// waitForEnd in investigation_test.go: this flow holds no open Stream, so it
// reads the transcript directly.
func waitForEndPolling(t *testing.T, h *harness.Harness, id string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, e := range fetchTranscript(t, h, id) {
			if e.Kind == "end" {
				return
			}
			if e.Kind == "error" {
				t.Fatalf("transcript carried an error envelope before end")
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("turn did not reach 'end' within %s", timeout)
}

type recordedCall struct {
	name  string
	input map[string]any
}

// toolCallsInOrder extracts the tool_use envelopes from the transcript in
// sequence.
func toolCallsInOrder(t *testing.T, h *harness.Harness, id string) []recordedCall {
	t.Helper()
	var out []recordedCall
	for _, e := range fetchTranscript(t, h, id) {
		if e.Kind == "tool_use" {
			out = append(out, recordedCall{name: e.ToolName, input: e.Input})
		}
	}
	return out
}

// byTool indexes round-trip results by tool name (last wins; each tool runs
// once in this flow).
func byTool(results []harness.ToolResult) map[string]harness.ToolResult {
	out := map[string]harness.ToolResult{}
	for _, r := range results {
		out[r.Tool] = r
	}
	return out
}

// namespaceNames pulls the namespace names out of a list_namespaces result.
func namespaceNames(t *testing.T, structured json.RawMessage) []string {
	t.Helper()
	var out struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(structured, &out); err != nil {
		t.Fatalf("decode list_namespaces structured result: %v (%s)", err, structured)
	}
	names := make([]string, 0, len(out.Items))
	for _, i := range out.Items {
		names = append(names, i.Name)
	}
	return names
}

// resourceNames pulls the item names out of a list_resources result.
func resourceNames(t *testing.T, structured json.RawMessage) []string {
	t.Helper()
	var out struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(structured, &out); err != nil {
		t.Fatalf("decode list_resources structured result: %v (%s)", err, structured)
	}
	names := make([]string, 0, len(out.Items))
	for _, i := range out.Items {
		names = append(names, i.Name)
	}
	return names
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// assertNoLiveK8sChild verifies no triagent-mcp --kind=k8s process from this
// run's bin dir is still alive after the turn ended. The stub spawns the k8s
// MCP as its own child and closes the session at replay end; a lingering
// process means a leak in the round-trip teardown (or a wedged MCP child).
// Scans /proc cmdlines — Linux-only, which matches the CI runner and the
// suite's envtest assumption.
func assertNoLiveK8sChild(t *testing.T) {
	t.Helper()
	binMarker := filepath.Join(harness.BinDir(), "triagent-mcp")
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Logf("registry check skipped: cannot read /proc: %v", err)
		return
	}
	var leaked []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue // process gone or not ours
		}
		cmdline := strings.ReplaceAll(string(raw), "\x00", " ")
		if strings.Contains(cmdline, binMarker) && strings.Contains(cmdline, "--kind=k8s") {
			leaked = append(leaked, "pid "+e.Name()+": "+strings.TrimSpace(cmdline))
		}
	}
	if len(leaked) > 0 {
		t.Errorf("k8s MCP child(ren) still alive after turn end (registry not clean):\n%s", strings.Join(leaked, "\n"))
	}
}
