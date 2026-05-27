// Package signalingest implements the triagent-mcp `signal-ingest` MCP server:
// four tools (query_signal_history, start_investigation, report_unclear,
// dismiss_items) that POST to a launcher loopback endpoint. Used by the
// short-lived ingestion agent the watches subsystem spawns per poll batch.
package signalingest

import (
	"context"
	"errors"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Options struct {
	LauncherURL   string       // TRIAGENT_MCP_TELEMETRY_URL
	LauncherToken string       // TRIAGENT_MCP_TELEMETRY_TOKEN
	TraceID       string       // TRIAGENT_MCP_TRACE_ID (watch id)
	HTTPClient    *http.Client // optional override; nil → http.DefaultClient
}

type Server struct {
	impl *mcp.Server
	opts Options
}

func New(opts Options) (*Server, error) {
	if opts.LauncherURL == "" {
		return nil, errors.New("LauncherURL required")
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = http.DefaultClient
	}
	impl := mcp.NewServer(&mcp.Implementation{
		Name:    "triagent-signal-ingest",
		Version: "0.1.0",
	}, nil)
	s := &Server{impl: impl, opts: opts}
	s.register()
	return s, nil
}

func (s *Server) Run(ctx context.Context) error {
	return s.impl.Run(ctx, &mcp.StdioTransport{})
}

// register adds the tool handlers. Each individual tool is wired in its own
// tool_*.go file via this method (which the per-tool register helpers extend).
func (s *Server) register() {
	s.registerHistory()
	s.registerStart()
	s.registerUnclear()
	s.registerDismiss()
}
