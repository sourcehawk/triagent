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
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// writeKubeconfigFixture creates a small kubeconfig at path containing
// the supplied context names. Each context binds a same-named cluster
// and authinfo so the file is well-formed enough for clientcmd to load.
func writeActiveContextKubeconfig(t *testing.T, path string, contexts ...string) {
	t.Helper()
	cfg := clientcmdapi.NewConfig()
	for _, name := range contexts {
		cfg.Clusters[name] = &clientcmdapi.Cluster{Server: "https://" + name + ".example:6443"}
		cfg.AuthInfos[name] = &clientcmdapi.AuthInfo{Token: "tok"}
		cfg.Contexts[name] = &clientcmdapi.Context{Cluster: name, AuthInfo: name}
	}
	require.NoError(t, clientcmd.WriteToFile(*cfg, path))
}

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
	kc := filepath.Join(dir, "kubeconfig")
	writeActiveContextKubeconfig(t, kc, "ctx-alpha")
	got, err := seedActiveContext(context.Background(), p, dir, kc, "x")
	require.NoError(t, err)
	assert.Equal(t, "ctx-alpha", got)
	body, err := os.ReadFile(filepath.Join(dir, k8s.ActiveContextFile))
	require.NoError(t, err)
	assert.Equal(t, "ctx-alpha", string(body))
}

func TestSeedActiveContext_NoOpWhenNothingResolved(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	got, err := seedActiveContext(context.Background(), nil, dir, "", "anything")
	require.NoError(t, err)
	assert.Equal(t, "", got)
	_, err = os.Stat(filepath.Join(dir, k8s.ActiveContextFile))
	assert.True(t, os.IsNotExist(err), "no file written when no kubeContext could be resolved")
}

// Regression: the provider's KubeContext for a cluster the operator has
// not logged into yet is not present in the frozen session kubeconfig
// (teleport ListClusters now synthesises a context name for every
// discoverable cluster). Pre-seeding active-context with such a name
// leaves the k8s MCP hydrate failing and the prom resolver later
// port-forwarding a non-existent context — degrade silently to the
// switch_context flow instead.
func TestSeedActiveContext_SkipsWhenContextMissingFromKubeconfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	kc := filepath.Join(dir, "kubeconfig")
	// Frozen session kubeconfig knows only ctx-logged-in.
	writeActiveContextKubeconfig(t, kc, "ctx-logged-in")
	// Provider advertises a different cluster the operator hasn't logged into.
	p := &stubClusterProvider{clusters: []auth.Cluster{
		{ID: "x", Name: "alpha", KubeContext: "ctx-not-logged-in"},
	}}
	got, err := seedActiveContext(context.Background(), p, dir, kc, "x")
	require.NoError(t, err)
	assert.Equal(t, "", got, "must not pre-seed a context the session kubeconfig does not contain")
	_, err = os.Stat(filepath.Join(dir, k8s.ActiveContextFile))
	assert.True(t, os.IsNotExist(err), "no active-context file written when the resolved context is absent from the kubeconfig")
}

// When the kubeconfig path is empty (provider absent, or caller doesn't
// know it yet), preserve the original best-effort behaviour: the resolved
// context is trusted because we cannot verify it.
func TestSeedActiveContext_WritesWhenKubeconfigPathEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := &stubClusterProvider{clusters: []auth.Cluster{
		{ID: "x", Name: "alpha", KubeContext: "ctx-alpha"},
	}}
	got, err := seedActiveContext(context.Background(), p, dir, "", "x")
	require.NoError(t, err)
	assert.Equal(t, "ctx-alpha", got)
	body, err := os.ReadFile(filepath.Join(dir, k8s.ActiveContextFile))
	require.NoError(t, err)
	assert.Equal(t, "ctx-alpha", string(body))
}
