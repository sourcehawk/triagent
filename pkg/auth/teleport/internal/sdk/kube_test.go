package sdk

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildClusterList_PopulatesKubeContext(t *testing.T) {
	t.Parallel()
	metas := []kubeServerMeta{
		{Name: "saas-dev-worker-2", Labels: map[string]string{"env": "dev", "full_id": "dev-gke-europe-north1-worker-2"}},
	}
	got := buildClusterList(metas, "camunda.teleport.sh")
	require := assert.New(t)
	require.Len(got, 1)
	require.Equal("saas-dev-worker-2", got[0].Name)
	require.Equal("dev-gke-europe-north1-worker-2", got[0].ID)
	// `<siteName>-<kubeCluster>` mirrors kubeContextName in kubeconfig.go.
	// Without this the launcher cannot resolve the operator's pre-
	// selected cluster_id back to a kubeconfig context.
	require.Equal("camunda.teleport.sh-saas-dev-worker-2", got[0].KubeContext)
}

func TestBuildClusterList_DedupAndSort(t *testing.T) {
	t.Parallel()
	metas := []kubeServerMeta{
		{Name: "saas-prod-a", Labels: map[string]string{"env": "prod"}},
		{Name: "saas-dev-b", Labels: map[string]string{"env": "dev"}},
		{Name: "saas-dev-a", Labels: map[string]string{"env": "dev"}},
		{Name: "saas-dev-b", Labels: map[string]string{"env": "dev"}}, // duplicate
		{Name: "saas-int-a", Labels: map[string]string{"env": "int"}},
	}
	got := buildClusterList(metas, "site")
	names := make([]string, 0, len(got))
	for _, c := range got {
		names = append(names, c.Name)
	}
	// dev < int < prod by env order; within each env, natural name order.
	assert.Equal(t, []string{"saas-dev-a", "saas-dev-b", "saas-int-a", "saas-prod-a"}, names)
}

func TestBuildClusterList_NilLabelsTolerated(t *testing.T) {
	t.Parallel()
	metas := []kubeServerMeta{{Name: "lonely", Labels: map[string]string{}}}
	got := buildClusterList(metas, "site")
	assert.Len(t, got, 1)
	assert.Equal(t, "", got[0].ID, "missing full_id label produces empty ID, not a panic")
	assert.Equal(t, "site-lonely", got[0].KubeContext)
}
