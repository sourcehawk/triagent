package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sourcehawk/triagent/pkg/auth"
	"github.com/sourcehawk/triagent/pkg/mcp/k8s"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubClusterProvider struct {
	clusters []auth.Cluster
	err      error
}

func (s *stubClusterProvider) ListClusters(_ context.Context) ([]auth.Cluster, error) {
	return s.clusters, s.err
}
func (s *stubClusterProvider) Login(_ context.Context, _ string) (*auth.LoginResult, error) {
	return nil, errors.New("not used in this test")
}
func (s *stubClusterProvider) IsAuthenticated() bool { return true }

func TestResolveActiveContext_NoProvider(t *testing.T) {
	t.Parallel()
	got := resolveActiveContext(context.Background(), nil, "anything")
	assert.Equal(t, "", got)
}

func TestResolveActiveContext_EmptyClusterID(t *testing.T) {
	t.Parallel()
	p := &stubClusterProvider{clusters: []auth.Cluster{{ID: "a", Name: "alpha", KubeContext: "ctx-alpha"}}}
	got := resolveActiveContext(context.Background(), p, "")
	assert.Equal(t, "", got)
}

func TestResolveActiveContext_MatchesByID(t *testing.T) {
	t.Parallel()
	p := &stubClusterProvider{clusters: []auth.Cluster{
		{ID: "dev-gke-europe-north1-worker-2", Name: "saas-dev-worker-2", KubeContext: "camunda.teleport.sh-saas-dev-worker-2"},
	}}
	got := resolveActiveContext(context.Background(), p, "dev-gke-europe-north1-worker-2")
	assert.Equal(t, "camunda.teleport.sh-saas-dev-worker-2", got)
}

// When the operator's cluster_id collides — one cluster's Name equals
// another cluster's ID — ID must win, regardless of iteration order.
// The picker sends `c.id` (full_id), so the ID match is the
// authoritative selection; falling back to a name-match on an earlier
// cluster would seed the wrong context. This test reproduces the
// collision with the ID-bearing cluster placed later in the slice so a
// single-pass `||` would incorrectly take the name-match first.
func TestResolveActiveContext_IDWinsOverNameWhenBothExist(t *testing.T) {
	t.Parallel()
	p := &stubClusterProvider{clusters: []auth.Cluster{
		{Name: "shared-token", ID: "first-cluster", KubeContext: "ctx-name-match"},
		{Name: "second", ID: "shared-token", KubeContext: "ctx-id-match"},
	}}
	got := resolveActiveContext(context.Background(), p, "shared-token")
	assert.Equal(t, "ctx-id-match", got,
		"ID match must win over a name match on an earlier cluster")
}

func TestResolveActiveContext_MatchesByName(t *testing.T) {
	t.Parallel()
	p := &stubClusterProvider{clusters: []auth.Cluster{
		{ID: "dev-gke-europe-north1-worker-2", Name: "saas-dev-worker-2", KubeContext: "camunda.teleport.sh-saas-dev-worker-2"},
	}}
	got := resolveActiveContext(context.Background(), p, "saas-dev-worker-2")
	assert.Equal(t, "camunda.teleport.sh-saas-dev-worker-2", got)
}

func TestResolveActiveContext_NoMatch(t *testing.T) {
	t.Parallel()
	p := &stubClusterProvider{clusters: []auth.Cluster{
		{ID: "a", Name: "alpha", KubeContext: "ctx-alpha"},
	}}
	got := resolveActiveContext(context.Background(), p, "nope")
	assert.Equal(t, "", got)
}

func TestResolveActiveContext_ListClustersErrorDegrades(t *testing.T) {
	t.Parallel()
	// A teleport hiccup must not fail preflight — the agent can always
	// call switch_context. Same fall-back as "no match".
	p := &stubClusterProvider{err: errors.New("teleport unreachable")}
	got := resolveActiveContext(context.Background(), p, "anything")
	assert.Equal(t, "", got)
}

func TestResolveActiveContext_MatchedClusterWithoutKubeContext(t *testing.T) {
	t.Parallel()
	// A kubeconfig provider that doesn't know the context (returns "")
	// must produce "" — the launcher won't pre-seed garbage.
	p := &stubClusterProvider{clusters: []auth.Cluster{
		{ID: "a", Name: "alpha"}, // KubeContext empty
	}}
	got := resolveActiveContext(context.Background(), p, "a")
	assert.Equal(t, "", got)
}

func TestSeedActiveContext_WritesFileWhenResolved(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := &stubClusterProvider{clusters: []auth.Cluster{
		{ID: "x", Name: "alpha", KubeContext: "ctx-alpha"},
	}}
	got, err := seedActiveContext(context.Background(), p, dir, "x")
	require.NoError(t, err)
	assert.Equal(t, "ctx-alpha", got)
	body, err := os.ReadFile(filepath.Join(dir, k8s.ActiveContextFile))
	require.NoError(t, err)
	assert.Equal(t, "ctx-alpha", string(body))
}

func TestSeedActiveContext_NoOpWhenNothingResolved(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	got, err := seedActiveContext(context.Background(), nil, dir, "anything")
	require.NoError(t, err)
	assert.Equal(t, "", got)
	_, err = os.Stat(filepath.Join(dir, k8s.ActiveContextFile))
	assert.True(t, os.IsNotExist(err), "no file written when no kubeContext could be resolved")
}
