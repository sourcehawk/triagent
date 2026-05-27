// Package meta implements the triagent-mcp `meta` MCP server: a single tool
// (`set_session_label`) the agent calls once early in the session to
// give the launcher a human-readable summary for the sidebar history
// list. The server is launcher-bound — it uses the telemetry env vars
// (TRIAGENT_MCP_TELEMETRY_URL, TRIAGENT_MCP_TRACE_ID, TRIAGENT_MCP_TELEMETRY_TOKEN) to
// reach a bearer-auth'd internal endpoint.
package meta

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/sourcehawk/triagent/pkg/mcp/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MaxLabelLen is the server-side cap. The sidebar's truncate-on-overflow
// pattern can take longer values, but capping early prevents pathological
// agent output from wedging the layout.
const MaxLabelLen = 80

// Options configures the meta server. LauncherURL/LauncherToken/TraceID
// are normally read from env (telemetry handshake); pass them in
// directly only for tests.
type Options struct {
	LauncherURL   string // base URL the launcher listens on; empty disables the tool
	LauncherToken string // bearer token to authenticate POSTs
	TraceID       string // launcher's investigation id (path segment)
	HTTPClient    *http.Client // optional override; nil → http.DefaultClient
}

// Server is the meta MCP server.
type Server struct {
	impl   *mcp.Server
	opts   Options
}

// New returns a configured server ready to Run. It does not validate
// LauncherURL — the tool surfaces a clear error per call when the
// launcher is unreachable, so a standalone smoke test (`triagent-mcp serve
// --kind=meta` outside the launcher) still boots.
func New(opts Options) (*Server, error) {
	if opts.HTTPClient == nil {
		opts.HTTPClient = http.DefaultClient
	}
	impl := mcp.NewServer(&mcp.Implementation{
		Name:    "triagent-mcp-meta",
		Version: "0.1.0",
	}, nil)
	s := &Server{impl: impl, opts: opts}
	s.register()
	return s, nil
}

// Run serves MCP requests over stdio until the client disconnects or ctx
// is cancelled.
func (s *Server) Run(ctx context.Context) error {
	return s.impl.Run(ctx, &mcp.StdioTransport{})
}

// setSessionLabelIn is the agent-supplied input shape for set_session_label.
type setSessionLabelIn struct {
	Label string `json:"label" jsonschema:"a 4-8 word summary of what's being investigated (symptom + scope, e.g. 'OOMKilled in zeebe-broker after 8.7 deploy'). Avoid cluster IDs and operator names — those render separately. Last write wins; you may refine later."`
}

type setSessionLabelOut struct {
	OK bool `json:"ok"`
}

// register adds the tool. Implemented in Task 2 (TDD).
func (s *Server) register() {
	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "set_session_label",
		Description: "Set the one-line summary the launcher's sidebar history list shows for this session. Call once early — typically after reading the operator's prompt and the first one or two MCP results — with a 4–8 word phrase capturing symptom + scope (e.g. \"OOMKilled in zeebe-broker after 8.7 deploy\"). Last write wins; you may refine later if your understanding changes. Don't include cluster IDs or operator names.",
	}, telemetry.Wrap("set_session_label", s.handleSetLabel))
}

// handleSetLabel validates the agent's input, posts the label to the
// launcher, and reports the outcome back to the agent. Validation
// failures and launcher errors come back as tool-level error results
// (caller agent receives a structured error string), not Go errors —
// matching the existing strategies/k8s pattern of `errorResult(...)`.
func (s *Server) handleSetLabel(ctx context.Context, _ *mcp.CallToolRequest, in setSessionLabelIn) (*mcp.CallToolResult, setSessionLabelOut, error) {
	clean, err := validateLabel(in.Label)
	if err != nil {
		return errorResult(err.Error()), setSessionLabelOut{}, nil
	}
	if err := s.postLabel(ctx, clean); err != nil {
		return errorResult(err.Error()), setSessionLabelOut{}, nil
	}
	return nil, setSessionLabelOut{OK: true}, nil
}

// errorResult wraps a message into a tool-level error CallToolResult.
// Mirrors the helper in mcp/internal/strategies/server.go (kept private
// to avoid pulling unrelated symbols across packages).
func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}

// launcherOrigin extracts scheme://host[:port] from a launcher URL. The
// per-session MCP config reuses TRIAGENT_MCP_TELEMETRY_URL which carries a
// full endpoint path (.../api/internal/tool-events); the meta MCP needs
// just the origin so it can append its own /api/internal/.../label
// path. Missing scheme or host is a configuration bug worth surfacing.
func launcherOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("URL missing scheme or host: %q", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

// validateLabel returns the trimmed, validated label or an error tagged
// with the user-facing message the tool should surface to the agent.
func validateLabel(label string) (string, error) {
	trimmed := strings.TrimSpace(label)
	if trimmed == "" {
		return "", fmt.Errorf("label cannot be empty")
	}
	if len(trimmed) > MaxLabelLen {
		return "", fmt.Errorf("label must be ≤%d characters (got %d)", MaxLabelLen, len(trimmed))
	}
	return trimmed, nil
}

// postLabel sends the validated label to the launcher's bearer-auth'd
// internal endpoint. Returns a non-nil error when the launcher is
// unreachable or returns non-2xx; the caller surfaces it as a tool
// error so the agent can adjust + retry.
//
// The TRIAGENT_MCP_TELEMETRY_URL env var carries the launcher's full
// telemetry endpoint path (e.g. http://127.0.0.1:PORT/api/internal/tool-events),
// so we extract the launcher's base origin (scheme + host) before
// appending the label endpoint path. Reusing the telemetry env keeps
// the per-session MCP config minimal — one origin, two paths.
func (s *Server) postLabel(ctx context.Context, label string) error {
	if s.opts.LauncherURL == "" || s.opts.TraceID == "" {
		return fmt.Errorf("launcher unreachable: meta MCP requires the launcher's telemetry env (TRIAGENT_MCP_TELEMETRY_URL, TRIAGENT_MCP_TRACE_ID)")
	}
	base, err := launcherOrigin(s.opts.LauncherURL)
	if err != nil {
		return fmt.Errorf("parse launcher URL: %w", err)
	}
	body, err := json.Marshal(map[string]string{"label": label})
	if err != nil {
		return fmt.Errorf("marshal label: %w", err)
	}
	url := base + "/api/internal/investigations/" + s.opts.TraceID + "/label"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.opts.LauncherToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.opts.LauncherToken)
	}
	resp, err := s.opts.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("post label: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("launcher returned %d for label POST", resp.StatusCode)
	}
	return nil
}
