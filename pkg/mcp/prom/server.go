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
	// Endpoint is the static Prometheus base URL. Required when
	// EndpointResolver is empty; optional otherwise.
	Endpoint string
	// Bearer is an optional Authorization: Bearer <token>. Mutually
	// exclusive with BasicAuth.
	Bearer string
	// BasicAuth is an optional "user:pass". Mutually exclusive with Bearer.
	BasicAuth string
	// HTTPClient is optional; defaults to a http.Client with 10s timeout.
	HTTPClient *http.Client

	// EndpointResolver, when non-empty, is a launcher / orchestrator
	// URL the MCP POSTs to before every tool call to obtain the
	// current Prom endpoint. Response shape: {"url": "..."} or
	// non-2xx with a body describing why no endpoint is available.
	// When set, the MCP's snapshot is replaced whenever the resolver
	// returns a URL that differs from the current snapshot's endpoint.
	EndpointResolver string
	// LauncherToken, when non-empty, is sent as `Authorization: Bearer
	// <token>` on resolver requests. Optional — resolvers that don't
	// require auth simply ignore it.
	LauncherToken string
}

// Server is the prom MCP server.
type Server struct {
	impl     *mcp.Server
	snapshot atomic.Pointer[snapshot]

	// resolver machinery (all optional; empty resolverURL → static mode)
	resolverURL   string
	launcherToken string
	httpClient    *http.Client
	bearer        string
	basic         string
}

// snapshot holds the per-binding state. Replaced wholesale on rebind so
// in-flight calls finish against the old endpoint without locking.
type snapshot struct {
	endpoint string
	client   *promClient
	catalog  *catalog
}

// New constructs a Server. It does NOT fetch the catalog — that happens
// in Run. An unreachable Prometheus endpoint is non-fatal: Run logs the
// failure to stderr and continues serving with an empty catalog, so the
// agent sees each tool return "catalog empty …" rather than the whole
// process exiting. Callers wanting a hard pre-check should validate the
// endpoint themselves before invoking Run.
func New(opts Options) (*Server, error) {
	if opts.Endpoint == "" && opts.EndpointResolver == "" {
		return nil, fmt.Errorf("endpoint or EndpointResolver is required")
	}
	if opts.Bearer != "" && opts.BasicAuth != "" {
		return nil, fmt.Errorf("bearer and BasicAuth are mutually exclusive")
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	impl := mcp.NewServer(&mcp.Implementation{
		Name:    "triagent-mcp-prom",
		Version: "0.1.0",
	}, nil)
	s := &Server{
		impl:          impl,
		resolverURL:   opts.EndpointResolver,
		launcherToken: opts.LauncherToken,
		httpClient:    httpClient,
		bearer:        opts.Bearer,
		basic:         opts.BasicAuth,
	}
	endpoint := opts.Endpoint
	if endpoint == "" {
		endpoint = "http://127.0.0.1:0" // unreachable placeholder; replaced by first resolver call
	}
	s.snapshot.Store(&snapshot{
		endpoint: endpoint,
		client:   newPromClient(endpoint, opts.Bearer, opts.BasicAuth, httpClient),
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
	// In static-endpoint mode, prime the catalog up-front so the first
	// tool call doesn't pay a refresh round-trip. In resolver mode,
	// skip — the resolver will be asked on the first tool call and
	// the catalog refresh will land alongside that, against whatever
	// URL the resolver returns. Refreshing against the placeholder
	// http://127.0.0.1:0 would just emit a noisy "fetch failed" line.
	if s.resolverURL == "" {
		if err := s.refreshCatalog(ctx); err != nil {
			_, _ = fmt.Fprintf(stderrWriter, "prom: initial catalog fetch failed: %v\n", err)
		}
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
		snap, err := s.currentSnapshot(ctx)
		if err != nil {
			return nil, err
		}
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

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "prom_describe_metric",
		Description: "Return label keys, sample values, related sibling metrics, and total cardinality for a known metric. Use after prom_list_metrics to learn the scope keys before querying. The first call against a fresh metric pays one HTTP round-trip; subsequent calls are cached.",
	}, telemetry.Wrap("prom_describe_metric", s.handleDescribeMetric))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "prom_query",
		Description: "Run an instant PromQL query. Scalar-first: prefer expressions that aggregate or top-N down to a small result. Hard cap of 50 series — over-cap responses are rejected with a corrective hint, never silently truncated. High-cardinality metrics MUST carry a non-`__name__` label matcher; unscoped references are rejected before the HTTP round-trip. Use prom_describe_metric to learn the scope keys for a given metric.",
	}, telemetry.Wrap("prom_query", s.handleQuery))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "prom_recent_value",
		Description: "Read the current value of `metric` for the exact label set `labels`. Returns a single value or a structured error (no data / multiple-series-matched-narrow-the-label-set). Preferred over composing PromQL when you know the labels.",
	}, telemetry.Wrap("prom_recent_value", s.handleRecentValue))

	mcp.AddTool(s.impl, &mcp.Tool{
		Name:        "prom_query_range",
		Description: "Run a range query when shape-over-time matters. Default response is a per-series summary (min/max/mean/percentiles/first/last + 20-cell sparkline). Step is auto-computed from range / max_points to stay inside the point budget. Series cap is 10 by default, hard ceiling 25. Same scope-enforcement rules as prom_query. Prefer prom_query for scalar/threshold questions; reach for this only when you genuinely need the time-shape.",
	}, telemetry.Wrap("prom_query_range", s.handleQueryRange))
}

type listMetricsIn struct {
	Query string `json:"query" jsonschema:"Required. Space-separated tokens; AND-matched against metric name OR HELP text (case-insensitive)."`
	Limit int    `json:"limit,omitempty" jsonschema:"Max matches to return; default 30, hard cap 50. Over-cap match sets come back as a facet breakdown, never truncated."`
}

func (s *Server) handleListMetrics(ctx context.Context, _ *mcp.CallToolRequest, in listMetricsIn) (*mcp.CallToolResult, SearchResult, error) {
	snap, err := s.currentSnapshot(ctx)
	if err != nil {
		return errorResult(err.Error()), SearchResult{Matches: []SearchMatch{}}, nil
	}
	if len(snap.catalog.names) == 0 {
		return errorResult("catalog empty — the endpoint may have no metrics indexed, or it is not yet reachable"), SearchResult{Matches: []SearchMatch{}}, nil
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

type describeMetricIn struct {
	Name string `json:"name" jsonschema:"Required. Exact metric name from prom_list_metrics output."`
}

func (s *Server) handleDescribeMetric(ctx context.Context, _ *mcp.CallToolRequest, in describeMetricIn) (*mcp.CallToolResult, DescribeResult, error) {
	snap, err := s.currentSnapshot(ctx)
	if err != nil {
		return errorResult(err.Error()), DescribeResult{Labels: []labelInfo{}}, nil
	}
	if len(snap.catalog.names) == 0 {
		return errorResult("catalog empty — the endpoint may have no metrics indexed, or it is not yet reachable"), DescribeResult{Labels: []labelInfo{}}, nil
	}
	res, err := describeMetric(ctx, snap.client, snap.catalog, in.Name)
	if err != nil {
		return errorResult(err.Error()), DescribeResult{Labels: []labelInfo{}}, nil
	}
	return nil, res, nil
}

type promQueryIn struct {
	Promql string `json:"promql" jsonschema:"Required. Instant PromQL. Prefer expressions that collapse to a scalar or ≤50-series vector — count(... > 0.9), topk(5, ...), max_over_time(...). Range-returning queries belong in prom_query_range."`
	Time   string `json:"time,omitempty" jsonschema:"Optional ISO-8601 / Unix-seconds. Defaults to now."`
}

func (s *Server) handleQuery(ctx context.Context, _ *mcp.CallToolRequest, in promQueryIn) (*mcp.CallToolResult, QueryResult, error) {
	snap, err := s.currentSnapshot(ctx)
	if err != nil {
		return errorResult(err.Error()), QueryResult{}, nil
	}
	if len(snap.catalog.names) == 0 {
		return errorResult("catalog empty — the endpoint may have no metrics indexed, or it is not yet reachable"), QueryResult{}, nil
	}
	res, err := runInstantQuery(ctx, snap, in.Promql, in.Time)
	if err != nil {
		return errorResult(err.Error()), QueryResult{}, nil
	}
	return nil, res, nil
}

type recentValueIn struct {
	Metric string            `json:"metric" jsonschema:"Required. Exact metric name from prom_list_metrics."`
	Labels map[string]string `json:"labels" jsonschema:"Required (non-empty). Label key/value pairs that uniquely identify the series to read."`
}

func (s *Server) handleRecentValue(ctx context.Context, _ *mcp.CallToolRequest, in recentValueIn) (*mcp.CallToolResult, ValueResult, error) {
	snap, err := s.currentSnapshot(ctx)
	if err != nil {
		return errorResult(err.Error()), ValueResult{}, nil
	}
	if len(snap.catalog.names) == 0 {
		return errorResult("catalog empty — the endpoint may have no metrics indexed, or it is not yet reachable"), ValueResult{}, nil
	}
	res, err := runRecentValue(ctx, snap, in.Metric, in.Labels)
	if err != nil {
		return errorResult(err.Error()), ValueResult{}, nil
	}
	return nil, res, nil
}

type promQueryRangeIn struct {
	Promql    string `json:"promql" jsonschema:"Required. Range PromQL. Prefer prom_query for scalar-or-small-vector questions; reach for prom_query_range only when shape-over-time matters."`
	Range     string `json:"range" jsonschema:"Required. Duration string (e.g. \"15m\", \"1h\"). Cap 24h."`
	End       string `json:"end,omitempty" jsonschema:"Optional ISO-8601 end time. Defaults to now."`
	MaxSeries int    `json:"max_series,omitempty" jsonschema:"Per-call series cap; default 10, hard ceiling 25."`
	MaxPoints int    `json:"max_points,omitempty" jsonschema:"Points-per-series budget; default 100, hard ceiling 200. Drives the auto-computed step."`
	Raw       bool   `json:"raw,omitempty" jsonschema:"When true, return raw [ts, value] points (capped to max_points). Default false → per-series summary stats + sparkline."`
}

func (s *Server) handleQueryRange(ctx context.Context, _ *mcp.CallToolRequest, in promQueryRangeIn) (*mcp.CallToolResult, RangeResult, error) {
	snap, err := s.currentSnapshot(ctx)
	if err != nil {
		return errorResult(err.Error()), RangeResult{Series: []RangeSeries{}}, nil
	}
	if len(snap.catalog.names) == 0 {
		return errorResult("catalog empty — the endpoint may have no metrics indexed, or it is not yet reachable"), RangeResult{Series: []RangeSeries{}}, nil
	}
	res, err := runRangeQuery(ctx, snap, in.Promql, in.Range, in.End, in.MaxSeries, in.MaxPoints, in.Raw)
	if err != nil {
		return errorResult(err.Error()), RangeResult{Series: []RangeSeries{}}, nil
	}
	return nil, res, nil
}

// errorResult formats a tool-level error result.
func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}
