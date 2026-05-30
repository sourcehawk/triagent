package parallel

import "github.com/sourcehawk/triagent/pkg/mcp/toolspec"

// ToolSpecs returns the parallel-server tool catalog. Mirrors the
// per-server pattern (k8s/strategies/prom/...) so the launcher's
// in-process catalog (internal/server/meta.go::toolCatalog) can
// aggregate without per-server casing.
func ToolSpecs() []toolspec.ToolSpec {
	return []toolspec.ToolSpec{
		{
			Server: "triagent-parallel",
			Name:   "call",
			Description: "Dispatch 2..8 independent MCP sub-calls in parallel and return their combined results. " +
				"Use this for batches of slow sub-agent tools (analyze_change, correlate_with_findings, propose_*) " +
				"whose answers are independent of each other. Provide a one-line `summary` describing the batch's " +
				"intent; it renders alongside the call so the operator sees what you're doing. Only allowlisted " +
				"tools are accepted; rejections come back per-item with rejected=true so other sub-calls still run.",
			Inputs: toolspec.FromStruct(CallIn{}),
		},
	}
}
