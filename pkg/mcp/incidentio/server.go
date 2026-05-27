// Package incidentio is an MCP server over the incident.io REST API. The
// server holds a single API key and exposes incident-aware tools; the
// agent passes an `incident_id` argument on every call so a single
// process can serve any incident the key has access to.
//
// Auth is sent as `Authorization: Bearer <key>` on every call. The key
// is supplied via `--incidentio-token` / `$TRIAGENT_MCP_INCIDENTIO_TOKEN` at
// boot. No incident scoping happens at boot — the operator's session
// scope flows through the system prompt to the agent, which threads it
// into each tool's `incident_id` argument.
package incidentio

import (
	"context"
	"fmt"
	"strings"

	"github.com/sourcehawk/triagent/pkg/mcp/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Options configures the incident.io MCP server.
type Options struct {
	// Token is the incident.io API key. Required.
	Token string

	// APIBase is the API base URL. Empty defaults to
	// "https://api.incident.io". Tests override this.
	APIBase string
}

// Server holds the MCP server and the HTTP client. Incidents are scoped
// per tool call (the agent passes `incident_id` via input).
type Server struct {
	impl   *mcp.Server
	opts   Options
	client *Client
}

// New constructs and registers the server. The token is the only
// required input; incident scoping is per call.
func New(_ context.Context, opts Options) (*Server, error) {
	if opts.Token == "" {
		return nil, fmt.Errorf("incidentio: token is required (set --incidentio-token or $TRIAGENT_MCP_INCIDENTIO_TOKEN)")
	}
	if opts.APIBase == "" {
		opts.APIBase = "https://api.incident.io"
	}
	opts.APIBase = strings.TrimRight(opts.APIBase, "/")

	client := NewClient(opts.APIBase, opts.Token)

	impl := mcp.NewServer(&mcp.Implementation{
		Name:    "triagent-mcp-incidentio",
		Version: "0.1.0",
	}, nil)
	s := &Server{
		impl:   impl,
		opts:   opts,
		client: client,
	}
	s.register()
	return s, nil
}

// Run serves MCP requests over stdio.
func (s *Server) Run(ctx context.Context) error {
	return s.impl.Run(ctx, &mcp.StdioTransport{})
}

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}

func (s *Server) register() {
	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "incidentio_get_incident",
		Description: "Read the full incident.io record for an incident: severity, status, timestamps, role assignments, custom field values (joined to field names). The single most-important call when ingesting incident context for a wiki entry. Pass `incident_id` (numeric reference like `5466` or UUID).",
	}, telemetry.Wrap("incidentio_get_incident", s.handleGetIncident))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "incidentio_get_timeline",
		Description: "Fetch the merged chronological timeline for an incident: incident updates (status changes, message-of-the-moment), action items, and follow-ups, sorted by time. Use for narrative reconstruction when drafting the wiki entry. Pass `incident_id`.",
	}, telemetry.Wrap("incidentio_get_timeline", s.handleGetTimeline))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "incidentio_get_postmortem",
		Description: "Fetch the post-incident review document for an incident, normalised to markdown. Returns empty when no postmortem exists yet (which is normal for live incidents). Often the densest source for root-cause and impact wording. Pass `incident_id`.",
	}, telemetry.Wrap("incidentio_get_postmortem", s.handleGetPostmortem))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "incidentio_search_related",
		Description: "Find prior incidents that share a custom-field value with the named incident — typically affected service or component. Useful for surfacing related incidents the wiki entry should cross-link. The `by` arg names the custom-field to join on (case-insensitive substring against field labels); `incident_id` names the source incident.",
	}, telemetry.Wrap("incidentio_search_related", s.handleSearchRelated))
}
