// Package prom implements the triagent-mcp `prom` MCP server: a bounded,
// scope-enforced, scalar-first surface over a single Prometheus endpoint.
// Discovery is keyword-search-only (no flat metric dump); queries are
// rejected when they reference high-cardinality metrics without a label
// matcher; every tool enforces hard output ceilings rather than
// truncating responses in place.
//
// The endpoint is held in an atomic pointer so the launcher can rebind
// the MCP on `switch_context` (wired by the launcher-integration plan).
// The catalog is rebuilt on rebind; in-flight tool calls complete against
// the snapshot they captured at entry.
package prom

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sourcehawk/triagent/pkg/mcp/telemetry"
)

// Options configures the prom MCP server.
type Options struct {
	// Endpoint is the Prometheus base URL (no trailing slash, no /api
	// suffix). Required.
	Endpoint string
	// Bearer is an optional Authorization: Bearer <token>. Mutually
	// exclusive with BasicAuth.
	Bearer string
	// BasicAuth is an optional "user:pass". Mutually exclusive with Bearer.
	BasicAuth string
	// HTTPClient is optional; defaults to a http.Client with 10s timeout.
	HTTPClient *http.Client
}

// Server is the prom MCP server.
type Server struct {
	impl     *mcp.Server
	snapshot atomic.Pointer[snapshot]
}

// snapshot holds the per-binding state. Replaced wholesale on rebind so
// in-flight calls finish against the old endpoint without locking.
type snapshot struct {
	endpoint string
	client   *promClient
	catalog  *catalog
}

// New constructs a Server. It does NOT fetch the catalog — that happens
// in Run, so a misconfigured endpoint fails loudly at startup instead of
// during the first tool call.
func New(opts Options) (*Server, error) {
	if opts.Endpoint == "" {
		return nil, fmt.Errorf("Endpoint is required")
	}
	if opts.Bearer != "" && opts.BasicAuth != "" {
		return nil, fmt.Errorf("Bearer and BasicAuth are mutually exclusive")
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	impl := mcp.NewServer(&mcp.Implementation{
		Name:    "triagent-mcp-prom",
		Version: "0.1.0",
	}, nil)
	s := &Server{impl: impl}
	s.snapshot.Store(&snapshot{
		endpoint: opts.Endpoint,
		client:   newPromClient(opts.Endpoint, opts.Bearer, opts.BasicAuth, httpClient),
		catalog:  emptyCatalog(),
	})
	s.register()
	return s, nil
}

// Run serves MCP requests over stdio. Fetches the initial catalog
// synchronously before serving so the first tool call sees a populated
// catalog when the endpoint is reachable. A failed initial fetch is
// logged but does not abort — the catalog stays empty and tools surface
// "catalog empty" to the agent.
func (s *Server) Run(ctx context.Context) error {
	if err := s.refreshCatalog(ctx); err != nil {
		// Non-fatal: surface to logs via the SDK's stderr; tools will
		// report "catalog empty" until a rebind succeeds.
		fmt.Fprintf(stderrWriter, "prom: initial catalog fetch failed: %v\n", err)
	}
	return s.impl.Run(ctx, &mcp.StdioTransport{})
}

// stderrWriter is a seam — tests override it to capture the warning line
// without racing on os.Stderr.
var stderrWriter = osStderr()

// register adds the tool and resource surface.
func (s *Server) register() {
	s.impl.AddResource(&mcp.Resource{
		URI:         "prom://info",
		Name:        "prom info",
		Description: "Indexed metric namespace summary + tool guidance. Read once at attach to learn what's available before issuing queries.",
		MIMEType:    "text/plain",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		snap := s.snapshot.Load()
		body := renderInfo(snap.catalog)
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{URI: "prom://info", MIMEType: "text/plain", Text: body},
			},
		}, nil
	})

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "prom_list_metrics",
		Description: "Search the indexed metric namespace by token-AND match against name and HELP text. Returns up to `limit` matches (cap 50). When more than the cap match, returns a sub-prefix facet breakdown instead of the first N — refine with more tokens or pick a sub-prefix and re-search. Required: non-empty, non-wildcard `query`.",
	}, telemetry.Wrap("prom_list_metrics", s.handleListMetrics))
}

type listMetricsIn struct {
	Query string `json:"query" jsonschema:"Required. Space-separated tokens; AND-matched against metric name OR HELP text (case-insensitive)."`
	Limit int    `json:"limit,omitempty" jsonschema:"Max matches to return; default 30, hard cap 50. Over-cap match sets come back as a facet breakdown, never truncated."`
}

func (s *Server) handleListMetrics(ctx context.Context, _ *mcp.CallToolRequest, in listMetricsIn) (*mcp.CallToolResult, SearchResult, error) {
	snap := s.snapshot.Load()
	if snap == nil || len(snap.catalog.names) == 0 {
		return errorResult("catalog empty — the endpoint may have no metrics indexed, or it is not yet reachable"), SearchResult{}, nil
	}
	r := searchMetrics(snap.catalog, in.Query, in.Limit)
	if r.Error != "" {
		return errorResult(r.Error), SearchResult{}, nil
	}
	return nil, r, nil
}

// refreshCatalog rebuilds the catalog from the current endpoint and
// atomically swaps the snapshot so in-flight tool calls continue
// against the old snapshot until they complete.
func (s *Server) refreshCatalog(ctx context.Context) error {
	snap := s.snapshot.Load()
	if snap == nil {
		return nil
	}
	cat, err := buildCatalog(ctx, snap.client)
	if err != nil {
		return err
	}
	// Build a new snapshot pointing at the same client and store it
	// wholesale; in-flight tool calls finish against the snapshot they
	// captured at entry.
	s.snapshot.Store(&snapshot{
		endpoint: snap.endpoint,
		client:   snap.client,
		catalog:  cat,
	})
	return nil
}

// errorResult formats a tool-level error result.
func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}
