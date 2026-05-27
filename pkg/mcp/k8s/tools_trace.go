package k8s

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// TraceCrossplaneInput selects the top-level Crossplane XR to expand.
type TraceCrossplaneInput struct {
	Namespace string `json:"namespace,omitempty" jsonschema:"Required only when the XR kind is namespaced (rare). Cluster-scoped XRs ignore this."`
	Kind      string `json:"kind" jsonschema:"Allow-listed Crossplane XR kind, e.g. XRemoteStorage."`
	Name      string `json:"name" jsonschema:"Name of the XR."`
}

// TraceNode is the compact per-resource view the tool renders. Everything a
// reader needs to decide 'is this the problem, or move on' without a full
// get_resource call — Ready/Synced status plus the provider-level message.
type TraceNode struct {
	Kind       string         `json:"kind"`
	Name       string         `json:"name"`
	Ready      string         `json:"ready,omitempty" jsonschema:"True, False, or Unknown."`
	Synced     string         `json:"synced,omitempty" jsonschema:"True, False, or Unknown."`
	Message    string         `json:"message,omitempty" jsonschema:"Latest condition message — usually the provider error when Ready/Synced is False."`
	AtProvider map[string]any `json:"atProvider,omitempty" jsonschema:"status.atProvider on the managed resource, when present. Carries external provider identifiers + state."`
}

type TraceCrossplaneOutput struct {
	XR       TraceNode   `json:"xr"`
	Children []TraceNode `json:"children"`
	// Truncated is set when the child list was capped.
	Truncated bool `json:"truncated,omitempty"`
}

const maxTraceChildren = 50

// traceCrossplane resolves an XR, reads its UID, then lists every Crossplane
// managed resource labelled `crossplane.io/composite=<uid>` across the
// configured provider groups. Returns a flat tree suitable for a quick
// "who's not Ready" scan.
func (t *ToolKit) traceCrossplane(ctx context.Context, _ *mcp.CallToolRequest, in TraceCrossplaneInput) (*mcp.CallToolResult, TraceCrossplaneOutput, error) {
	snap, errRes := t.activeSnapshot()
	if snap == nil {
		return errRes, TraceCrossplaneOutput{}, nil
	}
	rk, ok := snap.resolver.Lookup(in.Kind)
	if !ok {
		return errorResult(fmt.Sprintf("kind %q is not allow-listed; call list_resource_kinds for the available set", in.Kind)), TraceCrossplaneOutput{}, nil
	}
	if in.Name == "" {
		return errorResult("name is required"), TraceCrossplaneOutput{}, nil
	}
	if rk.Namespaced && in.Namespace == "" {
		return errorResult(fmt.Sprintf("trace_crossplane of namespaced kind %q requires `namespace`.", rk.Kind.Kind)), TraceCrossplaneOutput{}, nil
	}

	xr, err := t.getForKind(ctx, snap, rk, in.Namespace, in.Name)
	if err != nil {
		return errorResult(fmt.Sprintf("get %s %q: %v", rk.Kind.Kind, in.Name, err)), TraceCrossplaneOutput{}, nil
	}

	out := TraceCrossplaneOutput{
		XR: traceNodeFromUnstructured(rk.Kind.Kind, xr),
		// Initialise non-nil so an XR with no children marshals as
		// `"children":[]` instead of `"children":null`. Agents read
		// `null` as failure; `[]` reads correctly as "no children".
		Children: make([]TraceNode, 0),
	}

	uid := string(xr.GetUID())
	if uid == "" {
		return errorResult(fmt.Sprintf("%s %q has no metadata.uid — nothing to trace", rk.Kind.Kind, in.Name)), TraceCrossplaneOutput{}, nil
	}

	// dedupe identical (group/kind/name) hits across the two discovery
	// paths so a resource referenced by spec.resourceRefs AND carrying the
	// composite label isn't double-counted.
	seen := map[string]bool{}
	mark := func(group, kind, namespace, name string) string {
		key := group + "/" + kind + "/" + namespace + "/" + name
		if seen[key] {
			return ""
		}
		seen[key] = true
		return key
	}

	// Primary source: spec.resourceRefs[]. Crossplane writes one entry per
	// composed resource — this is what `crossplane beta trace` walks, and
	// it's authoritative even on clusters where the composite labeller
	// isn't running.
	refs, _, _ := unstructured.NestedSlice(xr.Object, "spec", "resourceRefs")
	for _, r := range refs {
		if len(out.Children) >= maxTraceChildren {
			out.Truncated = true
			break
		}
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		apiVersion, _ := m["apiVersion"].(string)
		kind, _ := m["kind"].(string)
		name, _ := m["name"].(string)
		namespace, _ := m["namespace"].(string)
		if apiVersion == "" || kind == "" || name == "" {
			continue
		}
		gv, err := schema.ParseGroupVersion(apiVersion)
		if err != nil {
			continue
		}
		if mark(gv.Group, kind, namespace, name) == "" {
			continue
		}
		plural, err := t.resolvePluralForKind(snap, gv, kind)
		if err != nil {
			out.Children = append(out.Children, TraceNode{
				Kind:    gv.Group + "/" + kind,
				Name:    name,
				Message: fmt.Sprintf("resolve plural: %v", err),
			})
			continue
		}
		gvr := gv.WithResource(plural)
		var obj *unstructured.Unstructured
		if namespace != "" {
			obj, err = snap.dynamic.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		} else {
			obj, err = snap.dynamic.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
		}
		if err != nil {
			out.Children = append(out.Children, TraceNode{
				Kind:    kind,
				Name:    name,
				Message: fmt.Sprintf("get failed: %v", err),
			})
			continue
		}
		out.Children = append(out.Children, traceNodeFromUnstructured(kind, obj))
	}

	// Backup: label scan across the configured provider groups. Picks up
	// anything resourceRefs missed (older XRs that haven't been
	// reconciled yet, or external controllers that label but don't
	// register a ref).
	if !out.Truncated {
		selector := "crossplane.io/composite=" + uid
		for _, gvr := range snap.crossplaneGVRs {
			list, err := snap.dynamic.Resource(gvr).List(ctx, metav1.ListOptions{LabelSelector: selector, Limit: int64(maxTraceChildren + 1)})
			if err != nil {
				// Don't abort — a single upset provider group shouldn't blind the
				// whole trace. Surface as a synthetic child so Claude sees why.
				out.Children = append(out.Children, TraceNode{
					Kind:    gvr.GroupResource().String(),
					Message: fmt.Sprintf("list failed: %v", err),
				})
				continue
			}
			for i := range list.Items {
				if len(out.Children) >= maxTraceChildren {
					out.Truncated = true
					break
				}
				item := &list.Items[i]
				if mark(gvr.Group, item.GetKind(), item.GetNamespace(), item.GetName()) == "" {
					continue
				}
				out.Children = append(out.Children, traceNodeFromUnstructured(item.GetKind(), item))
			}
			if out.Truncated {
				break
			}
		}
	}

	return nil, out, nil
}

// resolvePluralForKind asks the cluster's discovery client for the plural
// resource name backing (group/version, kind). Used to turn a
// spec.resourceRefs entry (which only carries apiVersion + kind + name)
// into the GVR the dynamic client needs.
func (t *ToolKit) resolvePluralForKind(snap *clientSnapshot, gv schema.GroupVersion, kind string) (string, error) {
	rl, err := snap.typed.Discovery().ServerResourcesForGroupVersion(gv.String())
	if err != nil {
		return "", err
	}
	for i := range rl.APIResources {
		ar := &rl.APIResources[i]
		// Subresources ("scale", "status") have a slash and aren't
		// what we want for a top-level Get.
		if strings.Contains(ar.Name, "/") {
			continue
		}
		if strings.EqualFold(ar.Kind, kind) {
			return ar.Name, nil
		}
	}
	return "", fmt.Errorf("no resource for kind %q in %s", kind, gv.String())
}

// traceNodeFromUnstructured extracts the Crossplane-idiomatic signals from
// a resource: conditions {Ready, Synced} + the latest non-empty message +
// status.atProvider (if present).
func traceNodeFromUnstructured(kind string, u *unstructured.Unstructured) TraceNode {
	n := TraceNode{Kind: kind, Name: u.GetName()}
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	var latestMessage string
	for _, c := range conds {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		typeStr, _ := cm["type"].(string)
		statusStr, _ := cm["status"].(string)
		switch typeStr {
		case "Ready":
			n.Ready = statusStr
		case "Synced":
			n.Synced = statusStr
		}
		// Pick the condition message most likely to be informative: prefer
		// False Ready/Synced messages; fall back to any non-empty message.
		if msg, _ := cm["message"].(string); msg != "" {
			if statusStr == "False" && (typeStr == "Ready" || typeStr == "Synced") {
				n.Message = msg
			} else if latestMessage == "" {
				latestMessage = msg
			}
		}
	}
	if n.Message == "" {
		n.Message = latestMessage
	}
	if atProvider, ok, _ := unstructured.NestedMap(u.Object, "status", "atProvider"); ok {
		n.AtProvider = atProvider
	}
	return n
}
