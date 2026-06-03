package k8s

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

// newTestKit wires a ToolKit with a fake dynamic client prepopulated with pods
// and a Pod-only resolver. The namespace is hard-coded to "app".
func newTestKit(t *testing.T, pods ...runtime.Object) *ToolKit {
	t.Helper()

	cs := fake.NewSimpleClientset()
	cs.Resources = []*metav1.APIResourceList{{
		GroupVersion: "v1",
		APIResources: []metav1.APIResource{{Name: "pods", Kind: "Pod", Namespaced: true}},
	}}
	allow := &Allowlist{Kinds: []Kind{{Version: "v1", Kind: "Pod", Description: "pods"}}}
	res, _, err := BuildResolver(cs.Discovery(), allow)
	require.NoError(t, err)

	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{
		{Group: "", Version: "v1", Resource: "pods"}: "PodList",
	}
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Version: "v1", Kind: "PodList"}, &unstructured.UnstructuredList{})
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, pods...)

	kit := &ToolKit{}
	kit.snapshot.Store(&clientSnapshot{
		resolver: res,
		dynamic:  dyn,
		typed:    cs,
	})
	return kit
}

func unstructuredPod(name, ns, phase string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("v1")
	u.SetKind("Pod")
	u.SetName(name)
	u.SetNamespace(ns)
	_ = unstructured.SetNestedField(u.Object, phase, "status", "phase")
	return u
}

func TestListResourceKinds_ReturnsAllowList(t *testing.T) {
	t.Parallel()
	kit := newTestKit(t)
	_, out, err := kit.listResourceKinds(context.Background(), nil, ListResourceKindsInput{})
	require.NoError(t, err)
	require.Len(t, out.Kinds, 1)
	assert.Equal(t, "Pod", out.Kinds[0].Kind)
	assert.Equal(t, "pods", out.Kinds[0].Description, "description not passed through")
	assert.Equal(t, "Namespaced", out.Kinds[0].Scope)
}

func newClusterScopedKit(t *testing.T, items ...runtime.Object) *ToolKit {
	t.Helper()

	cs := fake.NewSimpleClientset()
	cs.Resources = []*metav1.APIResourceList{{
		GroupVersion: "cloud.example.io/v1alpha1",
		APIResources: []metav1.APIResource{{Name: "remotestorages", Kind: "RemoteStorage", Namespaced: false}},
	}}
	allow := &Allowlist{Kinds: []Kind{{Group: "cloud.example.io", Version: "v1alpha1", Kind: "RemoteStorage"}}}
	res, _, err := BuildResolver(cs.Discovery(), allow)
	require.NoError(t, err)

	scheme := runtime.NewScheme()
	gvr := schema.GroupVersionResource{Group: "cloud.example.io", Version: "v1alpha1", Resource: "remotestorages"}
	gvk := schema.GroupVersionKind{Group: "cloud.example.io", Version: "v1alpha1", Kind: "RemoteStorage"}
	scheme.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "cloud.example.io", Version: "v1alpha1", Kind: "RemoteStorageList"}, &unstructured.UnstructuredList{})
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{gvr: "RemoteStorageList"}, items...)

	kit := &ToolKit{}
	kit.snapshot.Store(&clientSnapshot{
		resolver: res,
		dynamic:  dyn,
		typed:    cs,
	})
	return kit
}

func unstructuredRemoteStorage(name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("cloud.example.io/v1alpha1")
	u.SetKind("RemoteStorage")
	u.SetName(name)
	return u
}

func TestListResources_ClusterScopedReturnsAllItems(t *testing.T) {
	t.Parallel()
	kit := newClusterScopedKit(t,
		unstructuredRemoteStorage("abc-backup"),
		unstructuredRemoteStorage("abc-documents"),
		unstructuredRemoteStorage("xyz-backup"),
	)

	_, out, err := kit.listResources(context.Background(), nil, ListResourcesInput{Kind: "RemoteStorage"})
	require.NoError(t, err)
	assert.Len(t, out.Items, 3, "cluster-scoped list must return all items, not filter by cluster id")
}

func TestGetResource_ClusterScopedFetchesByName(t *testing.T) {
	t.Parallel()
	kit := newClusterScopedKit(t, unstructuredRemoteStorage("abc-backup"))

	r, _, err := kit.getResource(context.Background(), nil, GetResourceInput{Kind: "RemoteStorage", Name: "abc-backup"})
	require.NoError(t, err)
	require.False(t, r.IsError, "expected success, got error: %q", content(r))
	assert.Contains(t, content(r), "name: abc-backup", "render missing expected name")
}

func TestListResources_RejectsUnknownKind(t *testing.T) {
	t.Parallel()
	kit := newTestKit(t)
	res, _, err := kit.listResources(context.Background(), nil, ListResourcesInput{Kind: "Deployment"})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.True(t, res.IsError, "expected error result, got success")
	assert.Contains(t, content(res), "not allow-listed", "error msg lacks reason")
}

func TestListResources_ReturnsSummaries(t *testing.T) {
	t.Parallel()
	kit := newTestKit(t,
		unstructuredPod("zeebe-0", "app", "Running"),
		unstructuredPod("zeebe-1", "app", "Pending"),
	)
	_, out, err := kit.listResources(context.Background(), nil, ListResourcesInput{Kind: "Pod", Namespace: "app"})
	require.NoError(t, err)
	require.Len(t, out.Items, 2)
	assert.True(t,
		out.Items[0].Status["phase"] == "Running" || out.Items[1].Status["phase"] == "Running",
		"expected one Running pod, got %+v %+v", out.Items[0].Status, out.Items[1].Status)
}

func TestListResources_HonoursNamespaceScope(t *testing.T) {
	t.Parallel()
	kit := newTestKit(t,
		unstructuredPod("in-scope", "app", "Running"),
		unstructuredPod("out-of-scope", "other", "Running"),
	)
	_, out, err := kit.listResources(context.Background(), nil, ListResourcesInput{Kind: "Pod", Namespace: "app"})
	require.NoError(t, err)
	for _, it := range out.Items {
		assert.Equal(t, "app", it.Namespace, "leaked pod from %q namespace: %q", it.Namespace, it.Name)
	}
}

func TestListResources_RequiresNamespaceForNamespacedKind(t *testing.T) {
	t.Parallel()
	kit := newTestKit(t, unstructuredPod("zeebe-0", "app", "Running"))
	r, _, err := kit.listResources(context.Background(), nil, ListResourcesInput{Kind: "Pod"})
	require.NoError(t, err)
	require.True(t, r.IsError, "expected error when namespace omitted on namespaced kind")
	assert.Contains(t, content(r), "requires `namespace`")
}

func TestGetResource_NotFound(t *testing.T) {
	t.Parallel()
	kit := newTestKit(t)
	res, _, err := kit.getResource(context.Background(), nil, GetResourceInput{Kind: "Pod", Namespace: "app", Name: "ghost"})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected error result")
	assert.Contains(t, content(res), "not found", "message lacks 'not found'")
}

func TestGetResource_RequiresName(t *testing.T) {
	t.Parallel()
	kit := newTestKit(t)
	res, _, err := kit.getResource(context.Background(), nil, GetResourceInput{Kind: "Pod"})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected error result")
	assert.Contains(t, content(res), "name is required", "message lacks 'name is required'")
}

func TestGetResource_RequiresNamespaceForNamespacedKind(t *testing.T) {
	t.Parallel()
	kit := newTestKit(t, unstructuredPod("zeebe-0", "app", "Running"))
	r, _, err := kit.getResource(context.Background(), nil, GetResourceInput{Kind: "Pod", Name: "zeebe-0"})
	require.NoError(t, err)
	require.True(t, r.IsError)
	assert.Contains(t, content(r), "requires `namespace`")
}

func TestGetResource_RendersYAMLByDefault(t *testing.T) {
	t.Parallel()
	kit := newTestKit(t, unstructuredPod("zeebe-0", "app", "Running"))
	res, _, err := kit.getResource(context.Background(), nil, GetResourceInput{Kind: "Pod", Namespace: "app", Name: "zeebe-0"})
	require.NoError(t, err)
	require.False(t, res.IsError, "unexpected error: %q", content(res))
	out := content(res)
	assert.Contains(t, out, "name: zeebe-0", "yaml render missing expected fields")
	assert.Contains(t, out, "phase: Running", "yaml render missing expected fields")
}

func TestGetResource_ConfigMapRedactsSuspiciousValues(t *testing.T) {
	t.Parallel()

	// ConfigMap-only kit.
	cs := fake.NewSimpleClientset()
	cs.Resources = []*metav1.APIResourceList{{
		GroupVersion: "v1",
		APIResources: []metav1.APIResource{{Name: "configmaps", Kind: "ConfigMap", Namespaced: true}},
	}}
	allow := &Allowlist{Kinds: []Kind{{Version: "v1", Kind: "ConfigMap", Redact: true}}}
	res, _, err := BuildResolver(cs.Discovery(), allow)
	require.NoError(t, err)

	cm := &unstructured.Unstructured{}
	cm.SetAPIVersion("v1")
	cm.SetKind("ConfigMap")
	cm.SetName("app-config")
	cm.SetNamespace("app")
	_ = unstructured.SetNestedStringMap(cm.Object, map[string]string{
		"log_level": "debug",
		"api_key":   "abc-123-secret-key",
	}, "data")

	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{
		{Version: "v1", Resource: "configmaps"}: "ConfigMapList",
	}
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Version: "v1", Kind: "ConfigMapList"}, &unstructured.UnstructuredList{})
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, cm)

	kit := &ToolKit{}
	kit.snapshot.Store(&clientSnapshot{resolver: res, dynamic: dyn, typed: cs})
	result, _, err := kit.getResource(context.Background(), nil, GetResourceInput{Kind: "ConfigMap", Namespace: "app", Name: "app-config"})
	require.NoError(t, err)
	out := content(result)
	assert.NotContains(t, out, "abc-123-secret-key", "raw credential leaked")
	assert.True(t,
		strings.Contains(out, "api_key: '<redacted>'") || strings.Contains(out, "api_key: <redacted>"),
		"expected redacted api_key, got:\n%s", out)
	assert.Contains(t, out, "log_level: debug", "benign value was redacted")
}

func TestListResourceKinds_NoActiveContext_ReturnsHelpfulError(t *testing.T) {
	t.Parallel()
	kit := &ToolKit{} // no snapshot Store'd
	res, _, err := kit.listResourceKinds(context.Background(), nil, ListResourceKindsInput{})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.True(t, res.IsError)
	require.NotEmpty(t, res.Content)
	tc := res.Content[0].(*mcp.TextContent)
	assert.Contains(t, tc.Text, "no active kubernetes context")
	// Lead with the k8s-native path so a kubeconfig deployment (no Teleport
	// MCP) is steered at tools that actually exist.
	assert.Contains(t, tc.Text, "triagent-k8s.list_contexts")
	assert.Contains(t, tc.Text, "triagent-k8s.switch_context")
}

func content(r *mcp.CallToolResult) string {
	if r == nil || len(r.Content) == 0 {
		return ""
	}
	t, ok := r.Content[0].(*mcp.TextContent)
	if !ok {
		return ""
	}
	return t.Text
}

// --- lean-view test helpers -------------------------------------------------

// newPodAndConfigMapKit wires a ToolKit that allows both Pod and ConfigMap.
func newPodAndConfigMapKit(t *testing.T, items ...runtime.Object) *ToolKit {
	t.Helper()

	cs := fake.NewSimpleClientset()
	cs.Resources = []*metav1.APIResourceList{{
		GroupVersion: "v1",
		APIResources: []metav1.APIResource{
			{Name: "pods", Kind: "Pod", Namespaced: true},
			{Name: "configmaps", Kind: "ConfigMap", Namespaced: true},
		},
	}}
	allow := &Allowlist{Kinds: []Kind{
		{Version: "v1", Kind: "Pod"},
		{Version: "v1", Kind: "ConfigMap", Redact: true},
	}}
	res, _, err := BuildResolver(cs.Discovery(), allow)
	require.NoError(t, err)

	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{
		{Group: "", Version: "v1", Resource: "pods"}:       "PodList",
		{Group: "", Version: "v1", Resource: "configmaps"}: "ConfigMapList",
	}
	for _, gvk := range []schema.GroupVersionKind{
		{Version: "v1", Kind: "Pod"},
		{Version: "v1", Kind: "PodList"},
		{Version: "v1", Kind: "ConfigMap"},
		{Version: "v1", Kind: "ConfigMapList"},
	} {
		scheme.AddKnownTypeWithName(gvk, func() runtime.Object {
			if strings.HasSuffix(gvk.Kind, "List") {
				return &unstructured.UnstructuredList{}
			}
			return &unstructured.Unstructured{}
		}())
	}
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, items...)

	kit := &ToolKit{}
	kit.snapshot.Store(&clientSnapshot{resolver: res, dynamic: dyn, typed: cs})
	return kit
}

// podWithBloat returns an Unstructured Pod populated with managedFields,
// a noisy kubectl annotation and a minimal status — data for lean-stripping tests.
func podWithBloat() *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("v1")
	u.SetKind("Pod")
	u.SetName("pod-a")
	u.SetNamespace("ns1")
	u.SetUID("abc-123-uid")
	u.SetResourceVersion("12345")
	u.SetGeneration(3)
	_ = unstructured.SetNestedField(u.Object, []any{
		map[string]any{
			"manager":    "kubectl-client-side-apply",
			"operation":  "Update",
			"apiVersion": "v1",
		},
	}, "metadata", "managedFields")
	_ = unstructured.SetNestedStringMap(u.Object, map[string]string{
		"kubectl.kubernetes.io/last-applied-configuration": `{"apiVersion":"v1","kind":"Pod"}`,
	}, "metadata", "annotations")
	_ = unstructured.SetNestedField(u.Object, "Running", "status", "phase")
	_ = unstructured.SetNestedField(u.Object, []any{
		map[string]any{"type": "Ready", "status": "True"},
	}, "status", "conditions")
	return u
}

// primePod adds an unstructured pod object to the fake client's tracker.
func primePod(t *testing.T, kit *ToolKit, ns, name string, obj *unstructured.Unstructured) {
	t.Helper()
	obj.SetName(name)
	obj.SetNamespace(ns)
	snap := kit.snapshot.Load()
	dyn := snap.dynamic.(*dynfake.FakeDynamicClient)
	if err := dyn.Tracker().Add(obj); err != nil {
		t.Fatalf("prime pod: %v", err)
	}
}

// primePodWithAnnotations creates a bare pod with the given annotations and seeds it.
func primePodWithAnnotations(t *testing.T, kit *ToolKit, ns, name string, annotations map[string]string) {
	t.Helper()
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("v1")
	u.SetKind("Pod")
	u.SetName(name)
	u.SetNamespace(ns)
	u.SetAnnotations(annotations)
	primePod(t, kit, ns, name, u)
}

// primePodWithStatus creates a pod with a custom status map and seeds it.
func primePodWithStatus(t *testing.T, kit *ToolKit, ns, name string, status map[string]any) {
	t.Helper()
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("v1")
	u.SetKind("Pod")
	u.SetName(name)
	u.SetNamespace(ns)
	_ = unstructured.SetNestedMap(u.Object, status, "status")
	primePod(t, kit, ns, name, u)
}

// primeConfigMap creates a ConfigMap with the given data and seeds it.
func primeConfigMap(t *testing.T, kit *ToolKit, ns, name string, data map[string]string) {
	t.Helper()
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("v1")
	u.SetKind("ConfigMap")
	u.SetName(name)
	u.SetNamespace(ns)
	_ = unstructured.SetNestedStringMap(u.Object, data, "data")
	snap := kit.snapshot.Load()
	dyn := snap.dynamic.(*dynfake.FakeDynamicClient)
	if err := dyn.Tracker().Add(u); err != nil {
		t.Fatalf("prime configmap: %v", err)
	}
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// --- lean-view tests --------------------------------------------------------

func TestGetResource_LeanByDefault(t *testing.T) {
	t.Parallel()
	kit := newPodAndConfigMapKit(t)
	primePod(t, kit, "ns1", "pod-a", podWithBloat())
	res, _, _ := kit.getResource(t.Context(), nil, GetResourceInput{Kind: "Pod", Namespace: "ns1", Name: "pod-a"})
	out := content(res)
	if !strings.HasPrefix(out, "# lean view") {
		t.Fatalf("expected lean preamble, got first line: %q", firstLine(out))
	}
	// Check the YAML body (after the preamble line) doesn't contain the stripped fields.
	body := out[strings.Index(out, "\n")+1:]
	if strings.Contains(body, "managedFields") {
		t.Fatalf("lean default leaked managedFields:\n%s", body)
	}
	if strings.Contains(body, "kubectl.kubernetes.io/last-applied-configuration") {
		t.Fatalf("lean default leaked kubectl noise")
	}
}

func TestGetResource_RawIncludesEverything(t *testing.T) {
	t.Parallel()
	kit := newPodAndConfigMapKit(t)
	primePod(t, kit, "ns1", "pod-a", podWithBloat())
	res, _, _ := kit.getResource(t.Context(), nil, GetResourceInput{Kind: "Pod", Namespace: "ns1", Name: "pod-a", View: "raw"})
	out := content(res)
	if !strings.Contains(out, "managedFields") {
		t.Fatalf("raw view dropped managedFields")
	}
	if strings.HasPrefix(out, "# lean view") {
		t.Fatalf("raw view should not show lean preamble")
	}
}

func TestGetResource_LeanStripsRedundantAnnotations(t *testing.T) {
	t.Parallel()
	kit := newPodAndConfigMapKit(t)
	primePodWithAnnotations(t, kit, "ns1", "pod-a", map[string]string{
		"kubectl.kubernetes.io/last-applied-configuration": `{"big":"blob"}`,
		"meta.helm.sh/release-name":                        "example-release",
		"team":                                             "platform", // keep
	})
	res, _, _ := kit.getResource(t.Context(), nil, GetResourceInput{Kind: "Pod", Namespace: "ns1", Name: "pod-a"})
	out := content(res)
	// Strip the preamble line before asserting — the preamble itself names the stripped
	// annotation keys for documentation purposes, so checking full output would false-positive.
	body := out
	if i := strings.Index(out, "\n"); i >= 0 {
		body = out[i+1:]
	}
	if strings.Contains(body, "last-applied-configuration") {
		t.Fatalf("expected kubectl annotation to be stripped from body:\n%s", body)
	}
	if !strings.Contains(body, "team: platform") {
		t.Fatalf("expected custom annotation to survive:\n%s", body)
	}
}

func TestGetResource_ConfigMapRedactionStillApplies(t *testing.T) {
	t.Parallel()
	kit := newPodAndConfigMapKit(t)
	primeConfigMap(t, kit, "ns1", "cm", map[string]string{"password": "supersecret"})
	res, _, _ := kit.getResource(t.Context(), nil, GetResourceInput{Kind: "ConfigMap", Namespace: "ns1", Name: "cm"})
	out := content(res)
	if strings.Contains(out, "supersecret") {
		t.Fatalf("lean view leaked a redacted value")
	}
}

// TestGetResource_LeanCRDEmptyStatusRemovesKey verifies that when a pod's status
// contains only fields that projectStatus doesn't keep (e.g. observedGeneration),
// the entire status key is removed rather than serialised as "status: null".
func TestGetResource_LeanCRDEmptyStatusRemovesKey(t *testing.T) {
	t.Parallel()
	kit := newPodAndConfigMapKit(t)
	// Prime a Pod whose status contains only observedGeneration — a field that
	// projectStatus("Pod") does not include (it keeps phase/podIP/hostIP/startTime/
	// conditions/containerStatuses). This exercises the CRD-like empty-projection path.
	primePodWithStatus(t, kit, "ns1", "pod-a", map[string]any{
		"observedGeneration": int64(5),
	})
	res, _, _ := kit.getResource(t.Context(), nil, GetResourceInput{Kind: "Pod", Namespace: "ns1", Name: "pod-a"})
	out := content(res)
	// Strip the preamble line before asserting on the YAML body.
	body := out
	if i := strings.Index(out, "\n"); i >= 0 {
		body = out[i+1:]
	}
	if strings.Contains(body, "status: null") {
		t.Fatalf("expected status key removed, got 'status: null':\n%s", body)
	}
	if strings.Contains(body, "status:") {
		t.Fatalf("expected status key absent entirely:\n%s", body)
	}
	if strings.Contains(body, "observedGeneration") {
		t.Fatalf("expected observedGeneration stripped:\n%s", body)
	}
}
