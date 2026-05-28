package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"
)

// poster posts tool-call telemetry to the launcher's internal endpoint,
// standing in for the per-tool MCP server a real session would route
// through. Proposals reach the transcript only via this path — the
// launcher drops tool calls that arrive on claude's stdout stream — so a
// proposal action's telemetry POST is the load-bearing half of the
// round-trip. A nil poster means telemetry isn't configured (no
// MCP-config telemetry block); proposal actions then degrade to emitting
// the stream tool_use line only.
type poster struct {
	client    *http.Client
	url       string
	token     string
	traceID   string
	toolCount int
}

// toolEventBody mirrors the launcher's toolEventRequest
// (internal/server/handlers.go). Only the fields the stub sets are
// modelled; the launcher tolerates the omitted ones.
type toolEventBody struct {
	Phase    string          `json:"phase"`
	TraceID  string          `json:"traceId"`
	ToolID   string          `json:"toolId"`
	ToolName string          `json:"toolName,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
	Result   string          `json:"result,omitempty"`
}

// mcpConfigEnvSnippet is the minimal view of a launcher-written mcp.json
// the stub needs: each server's env block carries the telemetry triple.
type mcpConfigEnvSnippet struct {
	MCPServers map[string]struct {
		Env map[string]string `json:"env"`
	} `json:"mcpServers"`
}

// posterFromMCPConfig reads the telemetry triple from a launcher-written
// mcp.json. Every triagent-mcp server entry carries the same
// TRIAGENT_MCP_TELEMETRY_URL / _TOKEN / TRACE_ID env, so the first server
// with a non-empty URL wins. Returns nil (telemetry disabled) when the
// file is absent, unparseable, or carries no telemetry env — the stub
// then runs stream-only, the same as the production path with telemetry
// off.
func posterFromMCPConfig(path string) *poster {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg mcpConfigEnvSnippet
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	for _, srv := range cfg.MCPServers {
		url := srv.Env["TRIAGENT_MCP_TELEMETRY_URL"]
		if url == "" {
			continue
		}
		return &poster{
			client:  &http.Client{Timeout: 10 * time.Second},
			url:     url,
			token:   srv.Env["TRIAGENT_MCP_TELEMETRY_TOKEN"],
			traceID: srv.Env["TRIAGENT_MCP_TRACE_ID"],
		}
	}
	return nil
}

// nextToolID mints a unique tool id for one proposal round-trip so the
// launcher pairs the start and end events.
func (p *poster) nextToolID() string {
	p.toolCount++
	return fmt.Sprintf("stub-tool-%d", p.toolCount)
}

// roundTrip posts the start (with input) then end (with result body)
// tool-events for one proposal tool call, returning the toolID that
// linked them. The launcher publishes a tool_use envelope on start and a
// tool_result envelope on end; the frontend pairs them by toolID to
// render the proposal card.
func (p *poster) roundTrip(toolName string, input json.RawMessage, result string) (string, error) {
	id := p.nextToolID()
	if err := p.post(toolEventBody{
		Phase:    "start",
		TraceID:  p.traceID,
		ToolID:   id,
		ToolName: toolName,
		Input:    input,
	}); err != nil {
		return id, err
	}
	if err := p.post(toolEventBody{
		Phase:   "end",
		TraceID: p.traceID,
		ToolID:  id,
		Result:  result,
	}); err != nil {
		return id, err
	}
	return id, nil
}

func (p *poster) post(body toolEventBody) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, p.url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("tool-event POST %s phase=%s: status %d", p.url, body.Phase, resp.StatusCode)
	}
	return nil
}

// runGh invokes gh (resolved on $PATH — the gh-stub during the suite)
// with the given argv. A proposal whose backing MCP tool shells out to gh
// (create_github_issue) names that argv so the gh-stub records it,
// mirroring what the git MCP would do. Failures are returned so a missing
// gh fixture surfaces loudly. Empty argv is a no-op.
func runGh(argv []string) error {
	if len(argv) == 0 {
		return nil
	}
	cmd := exec.Command("gh", argv...)
	cmd.Stdout = os.Stderr // keep the stub's own stdout clean (stream-json only)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gh %v: %w", argv, err)
	}
	return nil
}
