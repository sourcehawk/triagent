package k8s

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestBuildResolver_MatchesAndDropsMissingKinds(t *testing.T) {
	t.Parallel()

	cs := fake.NewSimpleClientset()
	cs.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: "v1",
			APIResources: []metav1.APIResource{
				{Name: "pods", Kind: "Pod", Namespaced: true},
				{Name: "services", Kind: "Service", Namespaced: true},
			},
		},
		{
			GroupVersion: "apps/v1",
			APIResources: []metav1.APIResource{
				{Name: "deployments", Kind: "Deployment", Namespaced: true},
			},
		},
	}

	allow := &Allowlist{Kinds: []Kind{
		{Group: "", Version: "v1", Kind: "Pod", Description: "pods"},
		{Group: "", Version: "v1", Kind: "Service"},
		{Group: "apps", Version: "v1", Kind: "Deployment"},
		{Group: "zeebe.example.io", Version: "v1", Kind: "ZeebeCluster"}, // missing on cluster
	}}

	r, warnings, err := BuildResolver(cs.Discovery(), allow)
	require.NoError(t, err, "BuildResolver")
	assert.Len(t, r.All(), 3)
	if assert.Len(t, warnings, 1) {
		assert.Contains(t, warnings[0], "ZeebeCluster", "warning did not mention missing kind")
	}
}

func TestResolver_LookupVariants(t *testing.T) {
	t.Parallel()

	cs := fake.NewSimpleClientset()
	cs.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: "apps/v1",
			APIResources: []metav1.APIResource{
				{Name: "deployments", Kind: "Deployment", Namespaced: true},
			},
		},
	}
	allow := &Allowlist{Kinds: []Kind{{Group: "apps", Version: "v1", Kind: "Deployment"}}}

	r, _, err := BuildResolver(cs.Discovery(), allow)
	require.NoError(t, err)

	for _, q := range []string{"Deployment", "deployment", "apps/Deployment", "deployments", "DEPLOYMENTS"} {
		_, ok := r.Lookup(q)
		assert.True(t, ok, "lookup %q returned no match", q)
	}
	_, ok := r.Lookup("Pod")
	assert.False(t, ok, "lookup of unlisted kind should fail")
}

func TestResolver_ReportsClusterScopedKinds(t *testing.T) {
	t.Parallel()

	cs := fake.NewSimpleClientset()
	cs.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: "v1",
			APIResources: []metav1.APIResource{
				{Name: "nodes", Kind: "Node", Namespaced: false},
			},
		},
	}
	allow := &Allowlist{Kinds: []Kind{{Group: "", Version: "v1", Kind: "Node"}}}

	r, _, err := BuildResolver(cs.Discovery(), allow)
	require.NoError(t, err)
	rk, ok := r.Lookup("Node")
	require.True(t, ok, "Node not resolved")
	assert.False(t, rk.Namespaced, "Node should be cluster-scoped")
	assert.Equal(t, "Cluster", rk.Scope)
}
