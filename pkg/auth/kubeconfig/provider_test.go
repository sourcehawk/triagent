package kubeconfig

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fakeKubeconfig = `
apiVersion: v1
kind: Config
clusters:
- name: cluster-a
  cluster: { server: https://a.example.com }
- name: cluster-b
  cluster: { server: https://b.example.com }
contexts:
- name: ctx-a
  context: { cluster: cluster-a, user: alice }
- name: ctx-b
  context: { cluster: cluster-b, user: bob }
current-context: ctx-a
users:
- name: alice
  user: { token: xxx }
- name: bob
  user: { token: yyy }
`

func writeTempKubeconfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	require.NoError(t, os.WriteFile(path, []byte(fakeKubeconfig), 0600))
	return path
}

func TestListClusters_EnumeratesContexts(t *testing.T) {
	t.Setenv("KUBECONFIG", writeTempKubeconfig(t))
	p := NewProvider()
	clusters, err := p.ListClusters(context.Background())
	require.NoError(t, err)
	assert.Len(t, clusters, 2)
	names := []string{clusters[0].Name, clusters[1].Name}
	assert.ElementsMatch(t, []string{"ctx-a", "ctx-b"}, names)
}

func TestIsAuthenticated_AlwaysTrue(t *testing.T) {
	t.Setenv("KUBECONFIG", writeTempKubeconfig(t))
	p := NewProvider()
	assert.True(t, p.IsAuthenticated())
}

func TestLogin_WritesSubKubeconfigPinningContext(t *testing.T) {
	t.Setenv("KUBECONFIG", writeTempKubeconfig(t))
	p := NewProvider()
	res, err := p.Login(context.Background(), "ctx-b")
	require.NoError(t, err)
	// Login returns a path; load it and check current-context == ctx-b.
	data, err := os.ReadFile(res.ContextName)
	require.NoError(t, err)
	assert.Contains(t, string(data), "current-context: ctx-b")
}
