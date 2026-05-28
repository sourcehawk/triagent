package k8s

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/sourcehawk/triagent/pkg/mcp/k8sx"
	"github.com/sourcehawk/triagent/pkg/mcp/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// clientSnapshot is the immutable bundle every tool call operates against.
// switch_context atomically replaces it; in-flight calls finish against
// whichever snapshot they captured at handler entry.
type clientSnapshot struct {
	contextName    string
	resolver       *Resolver
	dynamic        dynamic.Interface
	typed          kubernetes.Interface
	crossplaneGVRs []schema.GroupVersionResource
	warnings       []string
}

// ToolKit bundles the dependencies every tool handler needs.
type ToolKit struct {
	kubeconfigPath string
	allowlist      *Allowlist
	crossplanePats []string
	sessionDir     string
	snapshot       atomic.Pointer[clientSnapshot]

	// openLogStream is an optional seam for unit tests. When nil, fetchOnePodLogs
	// opens the real Kubernetes log stream via the client snapshot. Tests inject
	// a function that returns a controlled body without talking to a real cluster.
	openLogStream func(ctx context.Context, snap *clientSnapshot, ns, pod string, opts *corev1.PodLogOptions) (io.ReadCloser, error)
}

// Options configure the MCP server. KubeconfigPath is required (used by
// switch_context to rebuild clients on demand).
type Options struct {
	KubeconfigPath          string
	AllowlistPath           string
	CrossplaneGroupPatterns []string
	// SessionDir is optional; required for streaming tools (stream_log_until,
	// search_log_stream). When empty those tools return an error explaining the
	// relaunch requirement.
	SessionDir string
}

// Server holds the configured MCP server and its supporting state.
type Server struct {
	impl    *mcp.Server
	toolkit *ToolKit
}

// New builds a Server. The toolkit boots context-less: no REST config, no
// clients, no discovery. Tools return a uniform "no active context" error
// until a successful switch_context populates the snapshot.
func New(_ context.Context, opts Options) (*Server, error) {
	if opts.KubeconfigPath == "" {
		return nil, fmt.Errorf("kubeconfig path is required")
	}
	allow, err := LoadAllowlist(opts.AllowlistPath)
	if err != nil {
		return nil, fmt.Errorf("load allow-list: %w", err)
	}
	patterns := opts.CrossplaneGroupPatterns
	if len(patterns) == 0 {
		patterns = DefaultCrossplaneGroupPatterns
	}
	toolkit := &ToolKit{
		kubeconfigPath: opts.KubeconfigPath,
		allowlist:      allow,
		crossplanePats: patterns,
		sessionDir:     opts.SessionDir,
	}
	impl := mcp.NewServer(&mcp.Implementation{
		Name:    "triagent-mcp-k8s",
		Version: "0.1.0",
	}, nil)
	toolkit.register(impl)
	return &Server{impl: impl, toolkit: toolkit}, nil
}

// Warnings reports non-fatal issues from the most recent context bind, or
// empty when no context is active.
func (s *Server) Warnings() []string {
	if snap := s.toolkit.snapshot.Load(); snap != nil {
		return snap.warnings
	}
	return nil
}

// Run serves MCP requests over stdio until ctx is cancelled. Before
// serving, opportunistically hydrate the active context from the
// launcher-written <sessionDir>/active-context — when the operator
// pre-selected a cluster on the session-start form the first agent
// tool call sees an active context without needing switch_context.
func (s *Server) Run(ctx context.Context) error {
	s.toolkit.hydrateFromActiveContext()
	return s.impl.Run(ctx, &mcp.StdioTransport{})
}

// noActiveContextMessage is the uniform error every existing tool returns
// when called before the first successful switch_context.
const noActiveContextMessage = "no active kubernetes context — call triagent-teleport.list_clusters → triagent-teleport.login → triagent-k8s.switch_context first"

// buildSnapshot constructs a fresh clientSnapshot for contextName. Returns
// the snapshot or an error; callers swap it onto the ToolKit only on
// success.
func (t *ToolKit) buildSnapshot(contextName string) (*clientSnapshot, error) {
	restCfg, err := k8sx.BuildRESTConfig(t.kubeconfigPath, contextName)
	if err != nil {
		return nil, fmt.Errorf("build rest config for context %q: %w", contextName, err)
	}
	dc, err := discovery.NewDiscoveryClientForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("discovery client: %w", err)
	}
	resolver, warnings, err := BuildResolver(dc, t.allowlist)
	if err != nil {
		return nil, fmt.Errorf("resolve allow-list: %w", err)
	}
	crossplaneGVRs, xpWarnings, err := DiscoverCrossplaneGVRs(dc, t.crossplanePats)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("crossplane discovery failed: %v", err))
	}
	warnings = append(warnings, xpWarnings...)
	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("dynamic client: %w", err)
	}
	typed, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("typed client: %w", err)
	}
	return &clientSnapshot{
		contextName:    contextName,
		resolver:       resolver,
		dynamic:        dyn,
		typed:          typed,
		crossplaneGVRs: crossplaneGVRs,
		warnings:       warnings,
	}, nil
}

// activeSnapshot returns the snapshot bound by the most recent successful
// switch_context, or nil + a populated error result when no context is
// active yet. Handlers use it as:
//
//	snap, errRes := t.activeSnapshot()
//	if snap == nil {
//	    return errRes, OutputType{}, nil
//	}
//
// so the no-active-context error message is uniform across every tool.
func (t *ToolKit) activeSnapshot() (*clientSnapshot, *mcp.CallToolResult) {
	snap := t.snapshot.Load()
	if snap == nil {
		return nil, errorResult(noActiveContextMessage)
	}
	return snap, nil
}

// register wires every tool exposed by this MCP server onto impl.
// list_contexts, get_current_context, and switch_context are added in
// Task B3.
func (t *ToolKit) register(impl *mcp.Server) {
	mcp.AddTool(impl, &mcp.Tool{
		Name:        "list_resource_kinds",
		Description: "Return the set of Kubernetes resource kinds this server will let you query. Call this first; use the 'description' field on each entry to decide which kinds are relevant to the investigation.",
	}, telemetry.Wrap("list_resource_kinds", t.listResourceKinds))

	mcp.AddTool(impl, &mcp.Tool{
		Name:        "list_resources",
		Description: "List instances of an allow-listed Kind. Namespaced kinds are scoped to the bound namespace. Cluster-scoped kinds are filtered to items whose metadata.name contains the cluster ID (so callers can't enumerate other tenants' cluster-scoped CRs). Returns per-item summaries, not full specs — use get_resource for the full object.",
	}, telemetry.Wrap("list_resources", t.listResources))

	mcp.AddTool(impl, &mcp.Tool{
		Name:        "get_resource",
		Description: "Return a single allow-listed resource by name, rendered as YAML (default), JSON, or a short describe-like form. For cluster-scoped kinds the name must contain the cluster ID.",
	}, telemetry.Wrap("get_resource", t.getResource))

	mcp.AddTool(impl, &mcp.Tool{
		Name:        "get_logs",
		Description: "Return recent log lines from one or more pods in the scoped namespace. Select the source via exactly one of: pod (name), labelSelector (e.g. app=my-service), or workload (e.g. Deployment/my-service, StatefulSet/my-db). Multi-pod output is prefixed with [pod]. Use grep to filter server-side; tailLines defaults to 200 per pod (cap 2000). Body is byte-capped per pod (default 32 KB, hard cap 256 KB); the # truncated: marker says when to tighten grep/sinceSeconds or escalate to stream_log_until.",
	}, telemetry.Wrap("get_logs", t.getLogs))

	mcp.AddTool(impl, &mcp.Tool{
		Name:        "list_events",
		Description: "List recent Kubernetes events in the scoped namespace, newest first. Optionally filter by involved object or type.",
	}, telemetry.Wrap("list_events", t.listEvents))

	mcp.AddTool(impl, &mcp.Tool{
		Name:        "list_namespaces",
		Description: "List Kubernetes namespaces visible to the active kubeconfig context. When no context is active yet, call triagent-k8s.switch_context first. By convention the workload namespace for a cluster is `<clusterID>-zeebe`, so a substring filter like a customer slug or cluster-id fragment usually finds it.",
	}, telemetry.Wrap("list_namespaces", t.listNamespaces))

	mcp.AddTool(impl, &mcp.Tool{
		Name:        "trace_crossplane",
		Description: "Expand a Crossplane composite (XR) into its composed managed resources and report each one's Ready/Synced condition plus status.atProvider. Use this when a RemoteStorage / EncryptedStorage / IAM-related CR is not Ready — the root cause usually lives on a provider managed resource, not on the top CR.",
	}, telemetry.Wrap("trace_crossplane", t.traceCrossplane))

	mcp.AddTool(impl, &mcp.Tool{
		Name:        "list_contexts",
		Description: "List every context defined in the kubeconfig. Marks the kubeconfig's current-context and the one this MCP is currently bound to (they can differ after switch_context).",
	}, telemetry.Wrap("list_contexts", t.listContexts))

	mcp.AddTool(impl, &mcp.Tool{
		Name:        "get_current_context",
		Description: "Return the context this MCP is currently bound to plus discovery warnings (e.g. allow-listed kinds not installed on the cluster). Errors when no context is active yet.",
	}, telemetry.Wrap("get_current_context", t.getCurrentContext))

	mcp.AddTool(impl, &mcp.Tool{
		Name:        "switch_context",
		Description: "Rebuild this MCP's clients against a different kubeconfig context. Discovery runs synchronously; on success the new context becomes active for all subsequent tool calls. On failure the previous context stays active.",
	}, telemetry.Wrap("switch_context", t.switchContext))

	mcp.AddTool(impl, &mcp.Tool{
		Name:        "stream_log_until",
		Description: "Tail one or more pods into a session-scoped file on disk and return a stream_id. Stops when a keyword group (AND-of-OR) matches or the timeout expires. The bulk log volume never enters the conversation — call search_log_stream with the stream_id to query it. Use this when get_logs hits the # truncated: marker and narrower grep/sinceSeconds won't help.",
	}, telemetry.Wrap("stream_log_until", t.streamLogUntil))

	mcp.AddTool(impl, &mcp.Tool{
		Name:        "search_log_stream",
		Description: "Filter a stream file previously captured by stream_log_until. Applies grep (case-sensitive substring) and/or kv-filters (key=value / key: value pairs, AND-joined). Returns a byte-capped excerpt — same truncation marker as get_logs.",
	}, telemetry.Wrap("search_log_stream", t.searchLogStream))
}
