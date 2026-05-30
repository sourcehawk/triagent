package meta

import "github.com/sourcehawk/triagent/pkg/mcp/toolspec"

// ToolSpecs returns the meta-server tool catalog. Mirrors the
// per-server pattern (k8s/strategies/prom/...) so the launcher's
// in-process catalog (internal/server/meta.go::toolCatalog) can
// aggregate without per-server casing.
func ToolSpecs() []toolspec.ToolSpec {
	return []toolspec.ToolSpec{
		{
			Server:      "triagent-meta",
			Name:        "set_session_label",
			Description: "Set a one-line summary for this chat session (shown in the sidebar history list). Call once early; last write wins.",
			Inputs:      toolspec.FromStruct(setSessionLabelIn{}),
		},
	}
}
