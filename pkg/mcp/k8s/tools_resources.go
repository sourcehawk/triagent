package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"
)

// --- list_resource_kinds ----------------------------------------------------

type ListResourceKindsInput struct{}

type ResourceKindInfo struct {
	Kind        string `json:"kind"`
	Group       string `json:"group,omitempty"`
	Version     string `json:"version"`
	Scope       string `json:"scope" jsonschema:"Namespaced or Cluster"`
	Description string `json:"description,omitempty"`
	Redacted    bool   `json:"redacted,omitempty" jsonschema:"true if values are redacted in get_resource output"`
}

type ListResourceKindsOutput struct {
	Kinds []ResourceKindInfo `json:"kinds"`
}

func (t *ToolKit) listResourceKinds(ctx context.Context, _ *mcp.CallToolRequest, _ ListResourceKindsInput) (*mcp.CallToolResult, ListResourceKindsOutput, error) {
	snap, errRes := t.activeSnapshot()
	if snap == nil {
		return errRes, ListResourceKindsOutput{}, nil
	}
	out := ListResourceKindsOutput{Kinds: make([]ResourceKindInfo, 0, len(snap.resolver.All()))}
	for _, rk := range snap.resolver.All() {
		out.Kinds = append(out.Kinds, ResourceKindInfo{
			Kind:        rk.Kind.Kind,
			Group:       rk.Kind.Group,
			Version:     rk.Kind.Version,
			Scope:       rk.Scope,
			Description: rk.Kind.Description,
			Redacted:    rk.Kind.Redact,
		})
	}
	return nil, out, nil
}

// --- list_resources ---------------------------------------------------------

type ListResourcesInput struct {
	Kind          string `json:"kind" jsonschema:"Kind name from list_resource_kinds. Accepts short form (Pod), group-qualified form (apps/Deployment), or plural (pods)."`
	Namespace     string `json:"namespace,omitempty" jsonschema:"Required for namespaced kinds. Pass the workload namespace from the Environment block's cluster-resource-namespace, or call list_namespaces to discover one. Ignored for cluster-scoped kinds."`
	LabelSelector string `json:"labelSelector,omitempty" jsonschema:"Kubernetes label selector — KEY=VALUE pairs comma-separated (AND-joined), e.g. 'app=zeebe' or 'app.kubernetes.io/component=zeebe-broker,app.kubernetes.io/instance=prod'. Keys must be labels that actually exist on the resource — read metadata.labels via get_resource first if you're unsure. Selectors that match nothing return an empty list silently."`
	FieldSelector string `json:"fieldSelector,omitempty" jsonschema:"field selector, e.g. status.phase=Running"`
	Limit         int    `json:"limit,omitempty" jsonschema:"max results; defaults to 100, hard cap 500"`
}

type ResourceSummary struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace,omitempty"`
	CreationTimestamp string            `json:"creationTimestamp,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Status            map[string]any    `json:"status,omitempty"`
}

type ListResourcesOutput struct {
	Kind  string            `json:"kind"`
	Items []ResourceSummary `json:"items"`
}

func (t *ToolKit) listResources(ctx context.Context, _ *mcp.CallToolRequest, in ListResourcesInput) (*mcp.CallToolResult, ListResourcesOutput, error) {
	snap, errRes := t.activeSnapshot()
	if snap == nil {
		return errRes, ListResourcesOutput{}, nil
	}
	rk, ok := snap.resolver.Lookup(in.Kind)
	if !ok {
		return errorResult(fmt.Sprintf("kind %q is not allow-listed; call list_resource_kinds for the available set", in.Kind)), ListResourcesOutput{}, nil
	}
	if rk.Namespaced && in.Namespace == "" {
		return errorResult(fmt.Sprintf("list_resources of namespaced kind %q requires `namespace`. If the Environment block has `cluster-resource-namespace` set, pass that. Otherwise call list_namespaces to find candidates.", rk.Kind.Kind)), ListResourcesOutput{}, nil
	}

	limit := in.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	opts := metav1.ListOptions{
		LabelSelector: in.LabelSelector,
		FieldSelector: in.FieldSelector,
		Limit:         int64(limit),
	}
	list, err := t.listForKind(ctx, snap, rk, in.Namespace, opts)
	if err != nil {
		return errorResult(fmt.Sprintf("list %s: %v", rk.Kind.GVK(), err)), ListResourcesOutput{}, nil
	}

	out := ListResourcesOutput{
		Kind:  rk.Kind.Kind,
		Items: make([]ResourceSummary, 0, len(list.Items)),
	}
	for i := range list.Items {
		out.Items = append(out.Items, summarize(&list.Items[i], rk))
	}
	return nil, out, nil
}

// --- get_resource -----------------------------------------------------------

type GetResourceInput struct {
	Kind      string `json:"kind" jsonschema:"Allow-listed kind."`
	Namespace string `json:"namespace,omitempty" jsonschema:"Required for namespaced kinds. Pass cluster-resource-namespace from the Environment block, or call list_namespaces. Ignored for cluster-scoped kinds."`
	Name      string `json:"name" jsonschema:"Resource name."`
	Output    string `json:"output,omitempty" jsonschema:"Rendering: yaml (default), json, or describe."`
	View      string `json:"view,omitempty" jsonschema:"lean (default) strips managedFields, kubectl/helm noise annotations, and projects status by kind; raw returns the unprojected object. Pass raw when the projection hides a field you need (e.g. a CRD with unusual status shape)."`
}

// leanStrippedAnnotationKeys are exact annotation keys removed in lean view.
var leanStrippedAnnotationKeys = []string{
	"kubectl.kubernetes.io/last-applied-configuration",
	"deployment.kubernetes.io/revision-history",
}

// leanStrippedAnnotationPrefixes are annotation key prefixes removed in lean view.
var leanStrippedAnnotationPrefixes = []string{
	"meta.helm.sh/",
}

// leanStrippedMetaFields are metadata fields removed in lean view.
var leanStrippedMetaFields = []string{
	"managedFields", "resourceVersion", "uid", "generation", "selfLink",
}

// applyLeanInPlace mutates obj in place, stripping noisy metadata fields,
// filtering annotations, and projecting status by kind.
func applyLeanInPlace(obj *unstructured.Unstructured, rk ResolvedKind) {
	for _, f := range leanStrippedMetaFields {
		unstructured.RemoveNestedField(obj.Object, "metadata", f)
	}
	if ann, ok, _ := unstructured.NestedStringMap(obj.Object, "metadata", "annotations"); ok {
		filtered := make(map[string]string, len(ann))
		for k, v := range ann {
			if slices.Contains(leanStrippedAnnotationKeys, k) {
				continue
			}
			if hasAnyPrefix(k, leanStrippedAnnotationPrefixes) {
				continue
			}
			filtered[k] = v
		}
		if len(filtered) == 0 {
			unstructured.RemoveNestedField(obj.Object, "metadata", "annotations")
		} else {
			_ = unstructured.SetNestedStringMap(obj.Object, filtered, "metadata", "annotations")
		}
	}
	if status, ok, _ := unstructured.NestedMap(obj.Object, "status"); ok && len(status) > 0 {
		projected := projectStatus(rk.Kind.Kind, status)
		if len(projected) == 0 {
			unstructured.RemoveNestedField(obj.Object, "status")
		} else {
			_ = unstructured.SetNestedMap(obj.Object, projected, "status")
		}
	}
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// getResource renders a single resource as text (YAML / JSON / describe).
// The return type is (CallToolResult, any, error) rather than a typed Out:
// with any+nil, the SDK does not populate StructuredContent, leaving Content
// as the sole payload. A typed empty-struct output would set
// StructuredContent="{}" and Claude clients that prefer StructuredContent
// would see an empty body instead of the rendered YAML.
func (t *ToolKit) getResource(ctx context.Context, _ *mcp.CallToolRequest, in GetResourceInput) (*mcp.CallToolResult, any, error) {
	snap, errRes := t.activeSnapshot()
	if snap == nil {
		return errRes, nil, nil
	}
	rk, ok := snap.resolver.Lookup(in.Kind)
	if !ok {
		return errorResult(fmt.Sprintf("kind %q is not allow-listed", in.Kind)), nil, nil
	}
	if in.Name == "" {
		return errorResult("name is required"), nil, nil
	}
	if rk.Namespaced && in.Namespace == "" {
		return errorResult(fmt.Sprintf("get_resource of namespaced kind %q requires `namespace`. Pass cluster-resource-namespace from the Environment block, or call list_namespaces to find candidates.", rk.Kind.Kind)), nil, nil
	}
	obj, err := t.getForKind(ctx, snap, rk, in.Namespace, in.Name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return errorResult(fmt.Sprintf("%s %q not found in namespace %q", rk.Kind.Kind, in.Name, in.Namespace)), nil, nil
		}
		return errorResult(fmt.Sprintf("get %s %q: %v", rk.Kind.Kind, in.Name, err)), nil, nil
	}

	if rk.Kind.Redact {
		applyRedactionInPlace(obj, rk)
	}

	view := strings.ToLower(strings.TrimSpace(in.View))
	if view == "" {
		view = "lean"
	}
	var preamble string
	switch view {
	case "lean":
		applyLeanInPlace(obj, rk)
		preamble = "# lean view — stripped: managedFields, resourceVersion, uid, generation, selfLink; kubectl last-applied-configuration, deployment revision-history, and helm-released annotations; status projected by kind. Pass view: \"raw\" for the full object.\n"
	case "raw":
		// no-op
	default:
		return errorResult(fmt.Sprintf("unknown view %q; use lean or raw", in.View)), nil, nil
	}

	output := strings.ToLower(strings.TrimSpace(in.Output))
	var rendered string
	switch output {
	case "", "yaml":
		b, err := yaml.Marshal(obj.Object)
		if err != nil {
			return errorResult(fmt.Sprintf("render yaml: %v", err)), nil, nil
		}
		rendered = string(b)
	case "json":
		b, err := json.MarshalIndent(obj.Object, "", "  ")
		if err != nil {
			return errorResult(fmt.Sprintf("render json: %v", err)), nil, nil
		}
		rendered = string(b)
	case "describe":
		b, err := yaml.Marshal(obj.Object)
		if err != nil {
			return errorResult(fmt.Sprintf("render describe: %v", err)), nil, nil
		}
		rendered = "# " + rk.Kind.Kind + " " + obj.GetName() + "\n" + string(b)
	default:
		return errorResult(fmt.Sprintf("unknown output %q; use yaml, json, or describe", in.Output)), nil, nil
	}

	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: preamble + rendered}}}, nil, nil
}

// --- helpers ---------------------------------------------------------------

func (t *ToolKit) listForKind(ctx context.Context, snap *clientSnapshot, rk ResolvedKind, namespace string, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	ri := resourceInterface(snap, rk, namespace)
	return ri.List(ctx, opts)
}

func (t *ToolKit) getForKind(ctx context.Context, snap *clientSnapshot, rk ResolvedKind, namespace, name string) (*unstructured.Unstructured, error) {
	ri := resourceInterface(snap, rk, namespace)
	return ri.Get(ctx, name, metav1.GetOptions{})
}

func resourceInterface(snap *clientSnapshot, rk ResolvedKind, namespace string) dynamic.ResourceInterface {
	res := snap.dynamic.Resource(rk.GVR)
	if rk.Namespaced {
		return res.Namespace(namespace)
	}
	return res
}

func summarize(u *unstructured.Unstructured, rk ResolvedKind) ResourceSummary {
	s := ResourceSummary{
		Name:      u.GetName(),
		Namespace: u.GetNamespace(),
		Labels:    u.GetLabels(),
	}
	if ts := u.GetCreationTimestamp(); !ts.IsZero() {
		s.CreationTimestamp = ts.UTC().Format("2006-01-02T15:04:05Z")
	}
	if status, ok, _ := unstructured.NestedMap(u.Object, "status"); ok && len(status) > 0 {
		s.Status = projectStatus(rk.Kind.Kind, status)
	}
	return s
}

// projectStatus trims the raw status map down to the fields most useful for
// a summary. Unknown kinds fall back to a tiny common subset.
func projectStatus(kind string, status map[string]any) map[string]any {
	switch strings.ToLower(kind) {
	case "pod":
		return pick(status, "phase", "podIP", "hostIP", "startTime", "conditions", "containerStatuses")
	case "deployment", "statefulset", "replicaset", "daemonset":
		return pick(status, "replicas", "readyReplicas", "availableReplicas", "updatedReplicas", "conditions")
	case "service":
		return pick(status, "loadBalancer")
	case "job", "cronjob":
		return pick(status, "active", "succeeded", "failed", "lastScheduleTime", "completionTime")
	}
	// Generic fallback.
	return pick(status, "conditions", "phase", "state", "ready")
}

func pick(m map[string]any, keys ...string) map[string]any {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// applyRedactionInPlace mutates obj to replace sensitive values before render.
// Only ConfigMap uses per-key heuristic redaction today; Secret would be here
// if we ever allow-listed it (we don't — LoadAllowlist strips it).
func applyRedactionInPlace(obj *unstructured.Unstructured, rk ResolvedKind) {
	if rk.Kind.Group != "" || rk.Kind.Kind != "ConfigMap" {
		return
	}
	raw, ok, _ := unstructured.NestedMap(obj.Object, "data")
	if !ok {
		return
	}
	str := make(map[string]string, len(raw))
	for k, v := range raw {
		if sv, isStr := v.(string); isStr {
			str[k] = sv
		}
	}
	for k, v := range RedactConfigMap(str) {
		if err := unstructured.SetNestedField(obj.Object, v, "data", k); err != nil {
			// If we can't set the field, strip it entirely — never serve a raw secret.
			_ = unstructured.SetNestedField(obj.Object, redactedValue, "data", k)
		}
	}
}

// errorResult constructs a tool result that signals failure to the model
// without aborting the MCP connection. The SDK surfaces this as IsError.
func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}
