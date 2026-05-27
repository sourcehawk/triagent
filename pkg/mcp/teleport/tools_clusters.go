package teleport

import (
	"context"
	"fmt"
	"sort"
	"strings"

	authteleport "github.com/sourcehawk/triagent/pkg/auth/teleport"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/client-go/tools/clientcmd"
)

// ListClustersInput is the input schema for list_clusters.
type ListClustersInput struct {
	Filter string `json:"filter,omitempty" jsonschema:"Optional case-sensitive substring matched against the cluster name OR any of its label values. Lets an alert that names a cluster in one shape (e.g. its cloud-provider identifier) find the Teleport cluster via the label that carries that shape."`
}

// ClusterInfo summarises one Teleport-reachable Kubernetes cluster.
type ClusterInfo struct {
	Name              string            `json:"name"`
	Labels            map[string]string `json:"labels,omitempty"`
	CurrentlyLoggedIn bool              `json:"currentlyLoggedIn" jsonschema:"True when a kubeconfig context already exists for this cluster."`
	// KubeContext is the kubeconfig context name to pass directly to
	// triagent-k8s.switch_context. Populated only when CurrentlyLoggedIn
	// is true. Differs from Name (Teleport's short cluster name) when
	// the proxy emits a prefixed context (e.g. "proxy.example.com-<name>").
	KubeContext string `json:"kubeContext,omitempty"`
}

// ListClustersOutput is the response schema for list_clusters.
type ListClustersOutput struct {
	Items []ClusterInfo `json:"items"`
}

// authRequiredMessage is the error returned when the operator's Teleport
// session is missing or expired. The proxy + connector come from the auth/teleport
// package so this message stays in sync with the actual `tsh login` invocation.
var authRequiredMessage = fmt.Sprintf(
	"Teleport session expired or missing — run `tsh login --proxy=%s --auth=%s` in your terminal, then retry.",
	authteleport.DefaultProxyAddr, authteleport.DefaultAuthConnector,
)

// listClusters enumerates Teleport-reachable Kubernetes clusters. Auth failures
// surface a clear message telling the operator which `tsh login` to run.
func (s *Server) listClusters(ctx context.Context, _ *mcp.CallToolRequest, in ListClustersInput) (*mcp.CallToolResult, ListClustersOutput, error) {
	if !s.provider.IsAuthenticated() {
		return errorResult(authRequiredMessage), ListClustersOutput{}, nil
	}
	clusters, err := s.provider.ListClusters(ctx)
	if err != nil {
		return errorResult(fmt.Sprintf("list teleport clusters: %v", err)), ListClustersOutput{}, nil
	}
	loggedIn, err := loggedInContextSubstrings(s.kubeconfigPath)
	if err != nil {
		// Don't fail the call on a kubeconfig read error — just report
		// every cluster as not-logged-in and let the operator notice.
		loggedIn = nil
	}
	out := ListClustersOutput{Items: make([]ClusterInfo, 0, len(clusters))}
	for _, c := range clusters {
		if in.Filter != "" && !matchesNameOrLabel(c.Name, c.Labels, in.Filter) {
			continue
		}
		kubeContext := findMatchingContext(c.Name, loggedIn)
		out.Items = append(out.Items, ClusterInfo{
			Name:              c.Name,
			Labels:            c.Labels,
			CurrentlyLoggedIn: kubeContext != "",
			KubeContext:       kubeContext,
		})
	}
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].Name < out.Items[j].Name })
	return nil, out, nil
}

// loggedInContextSubstrings reads kubeconfigPath and returns the names of
// every context defined there. We can't reconstruct the exact format
// kubeContextName(siteName, clusterName) emits without the SDK's siteName,
// so the matcher checks whether any context name CONTAINS the cluster
// name — close enough for "did I already log into this cluster?" UI.
func loggedInContextSubstrings(kubeconfigPath string) ([]string, error) {
	cfg, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(cfg.Contexts))
	for name := range cfg.Contexts {
		out = append(out, name)
	}
	return out, nil
}

// findMatchingContext returns the kubeconfig context name that corresponds
// to cluster (any context whose name contains the cluster name), or "" when
// none does. Used to populate ClusterInfo.KubeContext so the agent can pass
// it straight to triagent-k8s.switch_context without having to discover the
// proxy-prefixed format itself.
func findMatchingContext(cluster string, contextNames []string) string {
	for _, n := range contextNames {
		if strings.Contains(n, cluster) {
			return n
		}
	}
	return ""
}

// matchesNameOrLabel reports whether the filter substring appears in the
// cluster name OR in any of the label values. Lets an alert that names a
// cluster in one shape (its cloud-provider identifier) find the Teleport
// cluster via the label that carries that shape.
func matchesNameOrLabel(name string, labels map[string]string, filter string) bool {
	if strings.Contains(name, filter) {
		return true
	}
	for _, v := range labels {
		if strings.Contains(v, filter) {
			return true
		}
	}
	return false
}

// errorResult builds an MCP error result whose Content carries msg.
func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}
