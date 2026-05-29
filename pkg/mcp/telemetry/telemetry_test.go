package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestWrap_EndEventCarriesToolName pins the launcher-side contract: the
// phase=end event must carry toolName. The launcher's handleToolEvent
// reads it on end to decrement the MCP-health in-flight counter
// (recordEnd) and to fire the codefix live-persist branch; both
// silently no-op when it's empty.
func TestWrap_EndEventCarriesToolName(t *testing.T) {
	var (
		mu     sync.Mutex
		events []toolEvent
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev toolEvent
		if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
			t.Errorf("decode telemetry body: %v", err)
		}
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	prev := cfg
	t.Cleanup(func() { cfg = prev })
	cfg = &config{
		url:        srv.URL,
		traceID:    "trace-1",
		toolPrefix: "mcp__triagent-git-payments__",
		client:     &http.Client{Timeout: 2 * time.Second},
	}

	type in struct{}
	type out struct{}
	handler := func(context.Context, *mcp.CallToolRequest, in) (*mcp.CallToolResult, out, error) {
		return nil, out{}, nil
	}
	wrapped := Wrap("draft_pr", handler)
	if _, _, err := wrapped(context.Background(), &mcp.CallToolRequest{}, in{}); err != nil {
		t.Fatalf("wrapped handler returned error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 {
		t.Fatalf("got %d telemetry events, want 2 (start, end)", len(events))
	}
	start, end := events[0], events[1]
	if start.Phase != "start" || end.Phase != "end" {
		t.Fatalf("phases = %q, %q; want start, end", start.Phase, end.Phase)
	}
	want := "mcp__triagent-git-payments__draft_pr"
	if start.ToolName != want {
		t.Errorf("start toolName = %q, want %q", start.ToolName, want)
	}
	if end.ToolName != want {
		t.Errorf("end toolName = %q, want %q (launcher recordEnd + codefix persist branch on the end event)", end.ToolName, want)
	}
	if start.ToolID != end.ToolID {
		t.Errorf("start/end tool ids differ: %q vs %q", start.ToolID, end.ToolID)
	}
}
