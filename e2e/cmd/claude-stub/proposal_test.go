//go:build e2e

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// A "proposal" action models what a propose-* MCP tool does end to end:
// it POSTs a phase:start then a phase:end tool-event to the launcher's
// internal telemetry endpoint (the real path proposals reach the
// transcript through), and emits the matching stream-json tool_use line
// for fidelity. The launcher drops stream tool calls, so the telemetry
// POST is the load-bearing half — without it no proposal lands in the
// transcript. The full tool name and the end-phase result body are
// carried verbatim from the script.
func TestReplay_ProposalPostsStartAndEndToolEvents(t *testing.T) {
	var mu sync.Mutex
	var got []toolEventBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, ok := r.Header["Authorization"]; !ok || len(got) == 0 || got[0] != "Bearer tok-123" {
			t.Errorf("missing/wrong bearer: %v", r.Header["Authorization"])
		}
		var b toolEventBody
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Errorf("decode body: %v", err)
		}
		mu.Lock()
		got = append(got, b)
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	p := &poster{
		client:    srv.Client(),
		url:       srv.URL + "/api/internal/tool-events",
		token:     "tok-123",
		traceID:   "inv-xyz",
		toolCount: 0,
	}

	actions := []action{
		{
			Action: "proposal",
			Name:   "mcp__triagent-strategies__playbook_proposal_draft",
			Input:  json.RawMessage(`{"playbook_id":"investigation"}`),
			Result: json.RawMessage(`{"proposal_id":"p-1","playbook_id":"investigation","new_yaml":"id: investigation\n"}`),
		},
		{Action: "exit", Code: 0},
	}

	tr := &trace{f: os.NewFile(0, ""), enc: json.NewEncoder(&bytes.Buffer{})}
	var outBuf bytes.Buffer
	out := bufio.NewWriter(&outBuf)
	in := bufio.NewReader(strings.NewReader(""))

	if _, err := replayWith(actions, in, out, tr, p); err != nil {
		t.Fatalf("replay: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("got %d tool-events, want 2 (start+end): %+v", len(got), got)
	}
	if got[0].Phase != "start" || got[1].Phase != "end" {
		t.Fatalf("phases = %q,%q want start,end", got[0].Phase, got[1].Phase)
	}
	if got[0].TraceID != "inv-xyz" || got[0].ToolName != "mcp__triagent-strategies__playbook_proposal_draft" {
		t.Errorf("start event mismatch: %+v", got[0])
	}
	if got[0].ToolID == "" || got[0].ToolID != got[1].ToolID {
		t.Errorf("start/end toolId should match and be non-empty: %q vs %q", got[0].ToolID, got[1].ToolID)
	}
	if !strings.Contains(got[1].Result, `"proposal_id":"p-1"`) {
		t.Errorf("end result body missing payload: %q", got[1].Result)
	}
}

// A nil poster means telemetry isn't configured (the launcher ran the
// stub without an MCP-config telemetry block, e.g. the stub's own unit
// tests). A proposal action must then degrade to emitting the stream
// tool_use line only, never panicking on the absent poster.
func TestReplay_ProposalWithNilPosterIsStreamOnly(t *testing.T) {
	actions := []action{
		{
			Action: "proposal",
			Name:   "mcp__triagent-wiki__propose_wiki_draft",
			Result: json.RawMessage(`{"proposal_id":"w-1"}`),
		},
		{Action: "exit", Code: 0},
	}
	tr := &trace{f: os.NewFile(0, ""), enc: json.NewEncoder(&bytes.Buffer{})}
	var outBuf bytes.Buffer
	out := bufio.NewWriter(&outBuf)
	in := bufio.NewReader(strings.NewReader(""))

	if _, err := replayWith(actions, in, out, tr, nil); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if err := out.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if !strings.Contains(outBuf.String(), "tool_use") {
		t.Errorf("expected a stream tool_use line, got: %s", outBuf.String())
	}
}

// record_prompt slurps the entire stdin (the launcher feeds claude its
// prompt as one finite stdin write that then EOFs) and records it in the
// trace, so a test can assert the profile-derived system prompt reached
// the agent. Distinct from expect_*, which read a single line to model a
// stdin yield.
func TestReplay_RecordPromptCapturesFullStdin(t *testing.T) {
	prompt := "FIRST-LINE-MARKER\nsecond line\nthird line\n"
	actions := []action{
		{Action: "record_prompt"},
		{Action: "exit", Code: 0},
	}

	var traceBuf bytes.Buffer
	tr := &trace{f: os.NewFile(0, ""), enc: json.NewEncoder(&traceBuf)}
	var outBuf bytes.Buffer
	out := bufio.NewWriter(&outBuf)
	in := bufio.NewReader(strings.NewReader(prompt))

	if _, err := replayWith(actions, in, out, tr, nil); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !strings.Contains(traceBuf.String(), "FIRST-LINE-MARKER") {
		t.Errorf("trace did not record the prompt: %s", traceBuf.String())
	}
}

// posterFromMCPConfig extracts the telemetry triple (url/token/traceId)
// from a launcher-written mcp.json by reading any one server's env block —
// every server carries the same telemetry env. A config without telemetry
// env yields a nil poster (telemetry disabled), not an error.
func TestPosterFromMCPConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := dir + "/mcp.json"
	body := `{"mcpServers":{"triagent-strategies":{"command":"x","env":{` +
		`"TRIAGENT_MCP_TELEMETRY_URL":"http://127.0.0.1:9/api/internal/tool-events",` +
		`"TRIAGENT_MCP_TELEMETRY_TOKEN":"abc","TRIAGENT_MCP_TRACE_ID":"inv-7"}}}}`
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	p := posterFromMCPConfig(cfg)
	if p == nil {
		t.Fatal("expected a poster, got nil")
	}
	if p.token != "abc" || p.traceID != "inv-7" {
		t.Errorf("poster = %+v, want token=abc traceID=inv-7", p)
	}
	if !strings.HasSuffix(p.url, "/api/internal/tool-events") {
		t.Errorf("poster url = %q, want it to end in the tool-events path", p.url)
	}

	// No telemetry env → nil poster, no error.
	plain := dir + "/plain.json"
	if err := os.WriteFile(plain, []byte(`{"mcpServers":{"x":{"command":"y"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if posterFromMCPConfig(plain) != nil {
		t.Error("expected nil poster for a config without telemetry env")
	}
}
