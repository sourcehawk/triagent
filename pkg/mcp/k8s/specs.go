package k8s

import "github.com/sourcehawk/triagent/pkg/mcp/toolspec"

// ToolSpecs returns the k8s server's tool catalog with each tool's
// input shape introspected from its Go struct (and its jsonschema
// tags). See toolspec.ToolSpec for the format.
func ToolSpecs() []toolspec.ToolSpec {
	return []toolspec.ToolSpec{
		{
			Server:      "triagent-k8s",
			Name:        "list_resource_kinds",
			Description: "List the allow-listed resource kinds (call first).",
			Inputs:      toolspec.FromStruct(ListResourceKindsInput{}),
		},
		{
			Server:      "triagent-k8s",
			Name:        "list_resources",
			Description: "List instances of an allow-listed Kind, scoped to the bound namespace.",
			Inputs:      toolspec.FromStruct(ListResourcesInput{}),
		},
		{
			Server:      "triagent-k8s",
			Name:        "get_resource",
			Description: "Fetch one allow-listed resource as YAML / JSON / describe. Defaults to lean view (strips managedFields, kubectl/helm noise annotations, projects status by kind); pass view: \"raw\" for the unprojected object.",
			Inputs:      toolspec.FromStruct(GetResourceInput{}),
		},
		{
			Server:      "triagent-k8s",
			Name:        "get_logs",
			Description: "Fetch recent log lines for a pod / workload / labelSelector. Body is byte-capped per pod (default 32 KB, hard cap 256 KB) and truncated at line boundaries; a # truncated: marker tells the agent to tighten grep/sinceSeconds or escalate to stream_log_until.",
			Inputs:      toolspec.FromStruct(GetLogsInput{}),
		},
		{
			Server:      "triagent-k8s",
			Name:        "list_events",
			Description: "List Kubernetes events in the bound namespace.",
			Inputs:      toolspec.FromStruct(ListEventsInput{}),
		},
		{
			Server:      "triagent-k8s",
			Name:        "list_namespaces",
			Description: "List namespaces visible to the active kubeconfig context, optionally substring-filtered. Use to find the workload namespace (convention: `<clusterID>-zeebe`) when starting from a cluster id alone.",
			Inputs:      toolspec.FromStruct(ListNamespacesInput{}),
		},
		{
			Server:      "triagent-k8s",
			Name:        "trace_crossplane",
			Description: "Walk a Crossplane composite (XR) and report each child's Ready/Synced state.",
			Inputs:      toolspec.FromStruct(TraceCrossplaneInput{}),
		},
		{
			Server:      "triagent-k8s",
			Name:        "list_contexts",
			Description: "List every context in the kubeconfig. Marks the kubeconfig's current-context and the one this MCP is bound to (they can differ after switch_context).",
			Inputs:      toolspec.FromStruct(ListContextsInput{}),
		},
		{
			Server:      "triagent-k8s",
			Name:        "get_current_context",
			Description: "Return the context this MCP is bound to plus discovery warnings (allow-listed kinds not installed on the cluster). Errors when no context is active yet.",
			Inputs:      toolspec.FromStruct(GetCurrentContextInput{}),
		},
		{
			Server:      "triagent-k8s",
			Name:        "switch_context",
			Description: "Rebuild this MCP's clients against a different kubeconfig context. Discovery runs synchronously; on success the new context becomes active for all subsequent tool calls.",
			Inputs:      toolspec.FromStruct(SwitchContextInput{}),
		},
		{
			Server:      "triagent-k8s",
			Name:        "stream_log_until",
			Description: "Tail pod logs into a session-scoped file on disk; stop on keyword hit + grace or timeout. Returns a stream_id to pass to search_log_stream.",
			Inputs:      toolspec.FromStruct(StreamLogUntilInput{}),
		},
		{
			Server:      "triagent-k8s",
			Name:        "search_log_stream",
			Description: "Filter a stream file previously captured by stream_log_until with grep and/or kv-filters. Returns a byte-capped excerpt.",
			Inputs:      toolspec.FromStruct(SearchLogStreamInput{}),
		},
	}
}
