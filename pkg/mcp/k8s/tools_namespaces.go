package k8s

import (
	"context"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// --- list_namespaces -------------------------------------------------------

type ListNamespacesInput struct {
	Filter        string `json:"filter,omitempty" jsonschema:"Optional case-sensitive substring matched against the namespace name. Empty returns the first <limit> namespaces."`
	Limit         int    `json:"limit,omitempty" jsonschema:"Max results; defaults to 100, hard cap 500."`
	IncludeLabels bool   `json:"include_labels,omitempty" jsonschema:"Optional. When true, return labels and creationTimestamp for each namespace. Default false — names + phase only, which is ~10x smaller on prod clusters."`
}

type NamespaceInfo struct {
	Name              string            `json:"name"`
	CreationTimestamp string            `json:"creationTimestamp,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Phase             string            `json:"phase,omitempty"`
}

type ListNamespacesOutput struct {
	Items []NamespaceInfo `json:"items"`
}

const (
	defaultListNamespacesLimit = 100
	maxListNamespacesLimit     = 500
)

// listNamespaces returns the namespaces visible to the kubeconfig. Used by
// Claude when the session has no `cluster-resource-namespace` set —
// pair with the `<clusterID>-zeebe` convention to scope an investigation.
func (t *ToolKit) listNamespaces(ctx context.Context, _ *mcp.CallToolRequest, in ListNamespacesInput) (*mcp.CallToolResult, ListNamespacesOutput, error) {
	snap, errRes := t.activeSnapshot()
	if snap == nil {
		return errRes, ListNamespacesOutput{}, nil
	}
	limit := in.Limit
	if limit <= 0 {
		limit = defaultListNamespacesLimit
	}
	if limit > maxListNamespacesLimit {
		limit = maxListNamespacesLimit
	}

	list, err := snap.typed.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return errorResult("list namespaces: " + err.Error()), ListNamespacesOutput{}, nil
	}

	out := ListNamespacesOutput{Items: make([]NamespaceInfo, 0, len(list.Items))}
	for i := range list.Items {
		ns := &list.Items[i]
		if in.Filter != "" && !strings.Contains(ns.Name, in.Filter) {
			continue
		}
		info := NamespaceInfo{
			Name:  ns.Name,
			Phase: string(ns.Status.Phase),
		}
		if in.IncludeLabels {
			info.Labels = ns.Labels
			if ts := ns.CreationTimestamp; !ts.IsZero() {
				info.CreationTimestamp = ts.UTC().Format("2006-01-02T15:04:05Z")
			}
		}
		out.Items = append(out.Items, info)
		if len(out.Items) >= limit {
			break
		}
	}
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].Name < out.Items[j].Name })
	return nil, out, nil
}
