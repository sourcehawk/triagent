package parallel

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sourcehawk/triagent/pkg/mcp/telemetry"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Hard ceilings from the spec (§3.1, §9). The defaults are applied in
// clampConcurrency.
const (
	MinCalls           = 2
	MaxCalls           = 8
	DefaultConcurrency = 6
	MaxConcurrency     = 8
	MaxSummaryLen      = 120
	MaxPurposeLen      = 80
)

// Options builds a Server. registry is unexported (test seam); production
// callers go through NewFromEnv.
type Options struct {
	registry *Registry
}

// Server is the triagent-mcp parallel kind.
type Server struct {
	impl *sdkmcp.Server
	reg  *Registry
}

// NewFromEnv reads TRIAGENT_MCP_PARALLEL_UPSTREAMS from os.Environ and builds a
// production Server.
func NewFromEnv() (*Server, error) {
	upstreams, err := ParseUpstreams(os.Getenv(EnvUpstreams))
	if err != nil {
		return nil, err
	}
	return New(Options{registry: NewRegistry(upstreams)})
}

// New is the lower-level constructor — primarily a test seam.
func New(opts Options) (*Server, error) {
	if opts.registry == nil {
		return nil, fmt.Errorf("parallel.New: registry is required")
	}
	impl := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "triagent-mcp-parallel", Version: "0.1.0"}, nil)
	s := &Server{impl: impl, reg: opts.registry}
	sdkmcp.AddTool(impl, &sdkmcp.Tool{
		Name: "call",
		Description: "Dispatch 2..8 independent MCP sub-calls in parallel and return their combined results. " +
			"Use this for batches of slow sub-agent tools (analyze_change, research_codebase, correlate_with_findings, propose_*) " +
			"whose answers are independent of each other. Provide a one-line `summary` describing the batch's " +
			"intent; it renders alongside the call so the operator sees what you're doing. Only allowlisted " +
			"tools are accepted; rejections come back per-item with rejected=true so other sub-calls still run.",
	}, telemetry.Wrap("call", s.handleCall))
	return s, nil
}

// Run runs the server over stdio (production) or until ctx is cancelled.
// Closes the upstream registry on exit.
func (s *Server) Run(ctx context.Context) error {
	defer func() { _ = s.reg.Close() }()
	return s.impl.Run(ctx, &sdkmcp.StdioTransport{})
}

// handleCall is the tool handler. Validation → clamp → dispatch → response.
func (s *Server) handleCall(ctx context.Context, _ *sdkmcp.CallToolRequest, in CallIn) (*sdkmcp.CallToolResult, CallOut, error) {
	if err := validateInput(in); err != nil {
		// Out must still satisfy the schema (Results is a required array)
		// even when we're returning an IsError result, because the SDK
		// wrapper validates the structured payload regardless.
		return errorResult(err.Error()), CallOut{Results: []SubResult{}}, nil
	}
	maxConc := clampConcurrency(in.MaxConcurrency, len(in.Calls))
	start := time.Now()
	results := Dispatch(ctx, DispatchInput{
		Registry:       s.reg,
		Allowlist:      DefaultAllowlist(),
		Calls:          in.Calls,
		MaxConcurrency: maxConc,
		ParentToolID:   telemetry.CurrentToolID(ctx),
	})
	out := CallOut{
		Summary:    in.Summary,
		DurationMs: time.Since(start).Milliseconds(),
		Results:    results,
	}
	return nil, out, nil
}

// validateInput enforces the spec's hard limits (§3.1). Returned errors
// are user-facing — the handler emits them as IsError tool results so
// the agent can correct and retry.
func validateInput(in CallIn) error {
	if strings.TrimSpace(in.Summary) == "" {
		return fmt.Errorf("summary is required")
	}
	if len(in.Summary) > MaxSummaryLen {
		return fmt.Errorf("summary too long (got %d chars, max %d)", len(in.Summary), MaxSummaryLen)
	}
	if len(in.Calls) < MinCalls || len(in.Calls) > MaxCalls {
		return fmt.Errorf("calls must contain %d..%d items; got %d", MinCalls, MaxCalls, len(in.Calls))
	}
	if in.MaxConcurrency < 0 || in.MaxConcurrency > MaxConcurrency {
		return fmt.Errorf("max_concurrency must be in [1, %d]; got %d", MaxConcurrency, in.MaxConcurrency)
	}
	for i, c := range in.Calls {
		if c.Server == "" {
			return fmt.Errorf("calls[%d].server is required", i)
		}
		if c.Tool == "" {
			return fmt.Errorf("calls[%d].tool is required", i)
		}
		if len(c.Purpose) > MaxPurposeLen {
			return fmt.Errorf("calls[%d].purpose too long (got %d chars, max %d)", i, len(c.Purpose), MaxPurposeLen)
		}
	}
	return nil
}

// clampConcurrency applies the default + cap rules: 0 → default; clamped
// to len(calls); never exceeds MaxConcurrency.
func clampConcurrency(requested, numCalls int) int {
	if requested == 0 {
		requested = DefaultConcurrency
	}
	if requested > numCalls {
		requested = numCalls
	}
	if requested > MaxConcurrency {
		requested = MaxConcurrency
	}
	if requested < 1 {
		requested = 1
	}
	return requested
}

// errorResult builds an IsError tool result with a single text block,
// matching the pattern used by every other triagent-mcp server.
func errorResult(msg string) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{
		IsError: true,
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: msg}},
	}
}
