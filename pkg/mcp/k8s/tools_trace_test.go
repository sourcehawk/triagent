package k8s

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

// jsonMarshalForTest is a thin wrapper so the import is used only by
// tests that need raw-JSON shape assertions.
func jsonMarshalForTest(v any) ([]byte, error) { return json.Marshal(v) }

// newTraceKit spins up a ToolKit with:
//   - one XR kind (XRemoteStorage) in the allow-list
//   - one provider MR kind (storage.gcp.upbound.io/v1beta2 Bucket) wired into
//     t.crossplaneGVRs so the trace walker finds labelled children.
func newTraceKit(t *testing.T, items ...runtime.Object) *ToolKit {
	t.Helper()

	cs := fake.NewSimpleClientset()
	cs.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: "example.io/v1alpha1",
			APIResources: []metav1.APIResource{
				{Name: "xremotestorages", Kind: "XRemoteStorage", Namespaced: false},
			},
		},
		{
			GroupVersion: "storage.gcp.upbound.io/v1beta2",
			APIResources: []metav1.APIResource{
				{Name: "buckets", Kind: "Bucket", Namespaced: false},
			},
		},
	}
	allow := &Allowlist{Kinds: []Kind{
		{Group: "example.io", Version: "v1alpha1", Kind: "XRemoteStorage"},
	}}
	res, _, err := BuildResolver(cs.Discovery(), allow)
	require.NoError(t, err)

	xrGVR := schema.GroupVersionResource{Group: "example.io", Version: "v1alpha1", Resource: "xremotestorages"}
	bucketGVR := schema.GroupVersionResource{Group: "storage.gcp.upbound.io", Version: "v1beta2", Resource: "buckets"}

	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "example.io", Version: "v1alpha1", Kind: "XRemoteStorage"}, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "example.io", Version: "v1alpha1", Kind: "XRemoteStorageList"}, &unstructured.UnstructuredList{})
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "storage.gcp.upbound.io", Version: "v1beta2", Kind: "Bucket"}, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "storage.gcp.upbound.io", Version: "v1beta2", Kind: "BucketList"}, &unstructured.UnstructuredList{})
	gvrToListKind := map[schema.GroupVersionResource]string{
		xrGVR:     "XRemoteStorageList",
		bucketGVR: "BucketList",
	}
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, items...)

	kit := &ToolKit{}
	kit.snapshot.Store(&clientSnapshot{
		resolver:       res,
		dynamic:        dyn,
		typed:          cs,
		crossplaneGVRs: []schema.GroupVersionResource{bucketGVR},
	})
	return kit
}

func unstructuredXR(name, uid string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("example.io/v1alpha1")
	u.SetKind("XRemoteStorage")
	u.SetName(name)
	u.SetUID(types.UID(uid))
	_ = unstructured.SetNestedSlice(u.Object, []any{
		map[string]any{"type": "Ready", "status": "True", "message": "all good"},
		map[string]any{"type": "Synced", "status": "True"},
	}, "status", "conditions")
	return u
}

func unstructuredBucket(name, compositeUID, readyStatus, readyMessage string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("storage.gcp.upbound.io/v1beta2")
	u.SetKind("Bucket")
	u.SetName(name)
	u.SetLabels(map[string]string{"crossplane.io/composite": compositeUID})
	_ = unstructured.SetNestedSlice(u.Object, []any{
		map[string]any{"type": "Ready", "status": readyStatus, "message": readyMessage},
		map[string]any{"type": "Synced", "status": "True"},
	}, "status", "conditions")
	_ = unstructured.SetNestedField(u.Object, "projects/example/buckets/"+name, "status", "atProvider", "id")
	return u
}

func TestTraceCrossplane_ReturnsXRAndChildren(t *testing.T) {
	t.Parallel()
	kit := newTraceKit(t,
		unstructuredXR("abc-remote-storage", "uid-xr-1"),
		unstructuredBucket("backup-abc", "uid-xr-1", "False", "permission denied"),
		unstructuredBucket("document-abc", "uid-xr-1", "True", ""),
		unstructuredBucket("unrelated-bucket", "uid-other", "True", ""),
	)

	_, out, err := kit.traceCrossplane(context.Background(), nil, TraceCrossplaneInput{
		Kind: "XRemoteStorage",
		Name: "abc-remote-storage",
	})
	require.NoError(t, err)
	assert.Equal(t, "XRemoteStorage", out.XR.Kind)
	assert.Equal(t, "abc-remote-storage", out.XR.Name)
	assert.Equal(t, "True", out.XR.Ready)
	require.Len(t, out.Children, 2, "label filter failed?; children=%+v", out.Children)

	var gotFailed bool
	for _, c := range out.Children {
		if c.Name == "backup-abc" {
			gotFailed = true
			assert.Equal(t, "False", c.Ready)
			assert.Equal(t, "permission denied", c.Message)
			id, _ := c.AtProvider["id"].(string)
			assert.Equal(t, "projects/example/buckets/backup-abc", id)
		}
		assert.NotEqual(t, "unrelated-bucket", c.Name, "unrelated bucket leaked into trace")
	}
	assert.True(t, gotFailed, "failed bucket not in trace")
}

func TestTraceCrossplane_ClusterScopedFetchesByName(t *testing.T) {
	t.Parallel()
	kit := newTraceKit(t, unstructuredXR("xyz-remote-storage", "uid-1"))
	_, out, err := kit.traceCrossplane(context.Background(), nil, TraceCrossplaneInput{
		Kind: "XRemoteStorage",
		Name: "xyz-remote-storage",
	})
	require.NoError(t, err)
	assert.Equal(t, "xyz-remote-storage", out.XR.Name, "cluster-scoped trace must fetch by name without filtering")
}

// TestTraceCrossplane_EmptyChildrenMarshalAsArray verifies that an XR
// with no children emits `"children":[]` rather than `"children":null`.
// Agents read `null` as failure; `[]` reads unambiguously as "no
// children". JSON-shape fix only; semantics unchanged.
func TestTraceCrossplane_EmptyChildrenMarshalAsArray(t *testing.T) {
	t.Parallel()
	// XR with no resourceRefs and no labelled children: childless trace.
	kit := newTraceKit(t, unstructuredXR("lonely-xr", "uid-empty"))
	_, out, err := kit.traceCrossplane(context.Background(), nil, TraceCrossplaneInput{
		Kind: "XRemoteStorage",
		Name: "lonely-xr",
	})
	require.NoError(t, err)
	require.Empty(t, out.Children, "fixture XR has no children")

	raw, err := jsonMarshalForTest(out)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), `"children":null`,
		"empty children must marshal as `[]`, not `null`")
	assert.Contains(t, string(raw), `"children":[]`,
		"expected `\"children\":[]` in output, got: %s", raw)
}

// TestTraceCrossplane_FollowsResourceRefs proves the walker reads
// spec.resourceRefs[] (the canonical pointer crossplane writes on every
// XR, mirroring `crossplane beta trace`). Without this, clusters where
// the crossplane composite labeller isn't applied return empty children
// even though the resources exist.
func TestTraceCrossplane_FollowsResourceRefs(t *testing.T) {
	t.Parallel()
	// Two buckets — one referenced via spec.resourceRefs only (no label),
	// one only via label (no ref). Both should appear in the trace; the
	// "unrelated" bucket must not.
	xr := unstructuredXR("abc-remote-storage", "uid-xr-2")
	_ = unstructured.SetNestedSlice(xr.Object, []any{
		map[string]any{
			"apiVersion": "storage.gcp.upbound.io/v1beta2",
			"kind":       "Bucket",
			"name":       "ref-only-bucket",
		},
	}, "spec", "resourceRefs")

	refOnly := &unstructured.Unstructured{}
	refOnly.SetAPIVersion("storage.gcp.upbound.io/v1beta2")
	refOnly.SetKind("Bucket")
	refOnly.SetName("ref-only-bucket")
	_ = unstructured.SetNestedSlice(refOnly.Object, []any{
		map[string]any{"type": "Ready", "status": "False", "message": "perm denied"},
		map[string]any{"type": "Synced", "status": "True"},
	}, "status", "conditions")

	kit := newTraceKit(t, xr, refOnly,
		unstructuredBucket("label-only-bucket", "uid-xr-2", "True", ""),
		unstructuredBucket("unrelated-bucket", "uid-other", "True", ""),
	)
	_, out, err := kit.traceCrossplane(context.Background(), nil, TraceCrossplaneInput{
		Kind: "XRemoteStorage",
		Name: "abc-remote-storage",
	})
	require.NoError(t, err)
	names := map[string]bool{}
	for _, c := range out.Children {
		names[c.Name] = true
	}
	assert.True(t, names["ref-only-bucket"], "spec.resourceRefs-only child should be in trace; got %+v", out.Children)
	assert.True(t, names["label-only-bucket"], "label-only child should still appear via the backup scan; got %+v", out.Children)
	assert.False(t, names["unrelated-bucket"], "unrelated bucket leaked into trace")
}

func TestTraceCrossplane_UnknownKind(t *testing.T) {
	t.Parallel()
	kit := newTraceKit(t)
	r, _, err := kit.traceCrossplane(context.Background(), nil, TraceCrossplaneInput{
		Kind: "NotAThing",
		Name: "abc",
	})
	require.NoError(t, err)
	assert.True(t, r.IsError)
	assert.Contains(t, content(r), "not allow-listed", "expected allow-list refusal")
}

func TestGroupMatches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want bool
	}{
		{"storage.gcp.upbound.io", true},
		{"iam.aws.upbound.io", true},
		{"kubernetes.crossplane.io", true},
		{"apps", false},
		{"", false},
	}
	for _, tc := range cases {
		got := groupMatches(tc.name, DefaultCrossplaneGroupPatterns)
		assert.Equal(t, tc.want, got, "groupMatches(%q)", tc.name)
	}
}
