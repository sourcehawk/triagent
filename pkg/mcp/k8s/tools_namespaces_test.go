package k8s

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func newNamespacesKit(t *testing.T, names ...string) *ToolKit {
	t.Helper()
	objs := make([]runtime.Object, 0, len(names))
	for _, n := range names {
		objs = append(objs, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: n},
			Status:     corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		})
	}
	cs := fake.NewSimpleClientset(objs...)
	kit := &ToolKit{}
	kit.snapshot.Store(&clientSnapshot{typed: cs})
	return kit
}

func newNamespacesKitWithLabels(t *testing.T, labels map[string]map[string]string) *ToolKit {
	t.Helper()
	objs := make([]runtime.Object, 0, len(labels))
	for name, lbls := range labels {
		objs = append(objs, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:              name,
				Labels:            lbls,
				CreationTimestamp: metav1.Now(),
			},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		})
	}
	cs := fake.NewSimpleClientset(objs...)
	kit := &ToolKit{}
	kit.snapshot.Store(&clientSnapshot{typed: cs})
	return kit
}

func TestListNamespaces_NoFilterReturnsAll(t *testing.T) {
	t.Parallel()
	kit := newNamespacesKit(t, "abc-zeebe", "xyz-zeebe", "kube-system")
	_, out, err := kit.listNamespaces(context.Background(), nil, ListNamespacesInput{})
	require.NoError(t, err)
	require.Len(t, out.Items, 3)
}

func TestListNamespaces_FilterMatchesSubstring(t *testing.T) {
	t.Parallel()
	kit := newNamespacesKit(t, "abc-zeebe", "xyz-zeebe", "kube-system")
	_, out, err := kit.listNamespaces(context.Background(), nil, ListNamespacesInput{Filter: "zeebe"})
	require.NoError(t, err)
	require.Len(t, out.Items, 2)
	for _, it := range out.Items {
		assert.Contains(t, it.Name, "zeebe")
	}
}

func TestListNamespaces_LimitCapped(t *testing.T) {
	t.Parallel()
	names := make([]string, 0, 600)
	for i := 0; i < 600; i++ {
		names = append(names, fmt.Sprintf("ns-%04d", i))
	}
	kit := newNamespacesKit(t, names...)
	_, out, err := kit.listNamespaces(context.Background(), nil, ListNamespacesInput{Limit: 9999})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(out.Items), 500, "limit hard cap must be 500")
}

func TestListNamespaces_DefaultLimit(t *testing.T) {
	t.Parallel()
	names := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		names = append(names, fmt.Sprintf("ns-%04d", i))
	}
	kit := newNamespacesKit(t, names...)
	_, out, err := kit.listNamespaces(context.Background(), nil, ListNamespacesInput{})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(out.Items), 100, "default limit must be 100")
}

func TestListNamespaces_DefaultOmitsLabelsAndTimestamp(t *testing.T) {
	t.Parallel()
	kit := newNamespacesKitWithLabels(t, map[string]map[string]string{
		"abc-zeebe": {"team": "data", "env": "prod"},
	})
	_, out, err := kit.listNamespaces(context.Background(), nil, ListNamespacesInput{})
	require.NoError(t, err)
	require.Len(t, out.Items, 1)
	assert.Empty(t, out.Items[0].Labels, "labels must be omitted by default")
	assert.Empty(t, out.Items[0].CreationTimestamp, "creationTimestamp must be omitted by default")
	assert.Equal(t, "abc-zeebe", out.Items[0].Name)
}

func TestListNamespaces_IncludeLabelsReturnsLabelsAndTimestamp(t *testing.T) {
	t.Parallel()
	kit := newNamespacesKitWithLabels(t, map[string]map[string]string{
		"abc-zeebe": {"team": "data"},
	})
	_, out, err := kit.listNamespaces(context.Background(), nil, ListNamespacesInput{IncludeLabels: true})
	require.NoError(t, err)
	require.Len(t, out.Items, 1)
	assert.Equal(t, map[string]string{"team": "data"}, out.Items[0].Labels)
	assert.NotEmpty(t, out.Items[0].CreationTimestamp, "creationTimestamp must be set when labels requested")
}
