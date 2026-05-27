package parallel

import (
	"context"
	"fmt"
	"sync"

	"github.com/sourcehawk/triagent/pkg/mcp/telemetry"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// DispatchInput is what the broker hands to Dispatch — everything needed
// to fan out one batch.
type DispatchInput struct {
	Registry       *Registry
	Allowlist      Allowlist
	Calls          []SubCall
	MaxConcurrency int    // already clamped/defaulted by the caller (server.go)
	ParentToolID   string // current parallel_call tool id, for nested telemetry
}

// Dispatch fans out Calls in parallel up to MaxConcurrency, posting
// nested-telemetry start/end pairs per sub-call. Returns results
// positionally aligned with Calls.
func Dispatch(ctx context.Context, in DispatchInput) []SubResult {
	results := make([]SubResult, len(in.Calls))
	sem := make(chan struct{}, in.MaxConcurrency)
	var wg sync.WaitGroup
	for i, call := range in.Calls {
		i, call := i, call
		if !in.Allowlist.Allows(call.Server, call.Tool) {
			results[i] = SubResult{
				OK:       false,
				Rejected: true,
				Error:    fmt.Sprintf("tool %q on server %q not on parallel-call allowlist — invoke directly", call.Tool, call.Server),
			}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = runOne(ctx, in.Registry, call, in.ParentToolID)
		}()
	}
	wg.Wait()
	return results
}

// runOne executes a single sub-call against its upstream and wraps the
// outcome as a SubResult. Emits nested telemetry start/end against
// parentToolID so the activity panel renders the sub-call indented under
// the parallel_call card.
func runOne(ctx context.Context, reg *Registry, call SubCall, parentToolID string) SubResult {
	childID := telemetry.NewToolID()
	telemetry.SendNested(telemetry.NestedEvent{
		Phase:        "start",
		ParentToolID: parentToolID,
		ToolID:       childID,
		ToolName:     "mcp__" + call.Server + "__" + call.Tool,
		Input:        mergeInputForTelemetry(call.Input, call.Purpose),
	})

	res := SubResult{}
	sess, err := reg.Client(ctx, call.Server)
	if err != nil {
		res.OK = false
		res.Error = err.Error()
		telemetry.SendNested(telemetry.NestedEvent{
			Phase: "end", ParentToolID: parentToolID, ToolID: childID,
			ErrorText: err.Error(),
		})
		return res
	}

	toolRes, err := sess.CallTool(ctx, &sdkmcp.CallToolParams{Name: call.Tool, Arguments: call.Input})
	if err != nil {
		res.OK = false
		res.Error = fmt.Sprintf("%s/%s: %s", call.Server, call.Tool, err.Error())
		if ctx.Err() == context.DeadlineExceeded {
			res.TimedOut = true
		}
		telemetry.SendNested(telemetry.NestedEvent{
			Phase: "end", ParentToolID: parentToolID, ToolID: childID,
			ErrorText: res.Error,
		})
		return res
	}
	if toolRes.IsError {
		res.OK = false
		res.Error = fmt.Sprintf("%s/%s: %s", call.Server, call.Tool, firstTextContent(toolRes))
		telemetry.SendNested(telemetry.NestedEvent{
			Phase: "end", ParentToolID: parentToolID, ToolID: childID,
			Result: firstTextContent(toolRes), ErrorText: res.Error,
		})
		return res
	}

	// Prefer the structured content (matches what Claude sees); fall back
	// to the rendered text.
	res.OK = true
	if toolRes.StructuredContent != nil {
		res.Result = toolRes.StructuredContent
	} else {
		res.Result = map[string]any{"text": firstTextContent(toolRes)}
	}
	telemetry.SendNested(telemetry.NestedEvent{
		Phase: "end", ParentToolID: parentToolID, ToolID: childID,
		Result: firstTextContent(toolRes),
	})
	return res
}

// firstTextContent pulls the first TextContent block out of a tool
// result, or returns "" if none. Mirrors what the activity panel
// already does for rendered tool output elsewhere.
func firstTextContent(res *sdkmcp.CallToolResult) string {
	for _, c := range res.Content {
		if t, ok := c.(*sdkmcp.TextContent); ok {
			return t.Text
		}
	}
	return ""
}

// mergeInputForTelemetry returns a shallow copy of input with `purpose`
// added when non-empty. The activity panel's oneLineSummary lists each
// input key — adding `purpose` here surfaces it on the nested row.
func mergeInputForTelemetry(input map[string]any, purpose string) map[string]any {
	if purpose == "" {
		return input
	}
	out := make(map[string]any, len(input)+1)
	for k, v := range input {
		out[k] = v
	}
	out["purpose"] = purpose
	return out
}
