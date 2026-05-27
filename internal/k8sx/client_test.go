package k8sx

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// minimalKubeconfig writes a syntactically-valid kubeconfig to a temp file
// and returns the path. The cluster server URL is unreachable; that's fine —
// New only loads the config and builds a clientset, neither of which makes
// network calls.
func minimalKubeconfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig")
	body := []byte(`apiVersion: v1
kind: Config
current-context: test
clusters:
- name: test-cluster
  cluster:
    server: https://127.0.0.1:1
    insecure-skip-tls-verify: true
contexts:
- name: test
  context:
    cluster: test-cluster
    user: test-user
users:
- name: test-user
  user:
    token: ""
`)
	require.NoError(t, os.WriteFile(path, body, 0o600))
	return path
}

// TestNew_AllowsEmptyNamespace is a regression guard for the scope-unknown
// startup path. Before commit 5a8071c the launcher rejected sessions started
// with no clusterId at preflight time with "kube client: namespace is
// required". This test asserts that New no longer enforces that and returns
// a usable Client whose Namespace is empty.
func TestNew_AllowsEmptyNamespace(t *testing.T) {
	t.Parallel()
	kubeconfig := minimalKubeconfig(t)

	client, err := New(kubeconfig, "test", "")
	require.NoError(t, err, "New must accept empty namespace for scope-unknown sessions")
	require.NotNil(t, client)
	assert.Empty(t, client.Namespace, "empty namespace must be preserved on Client")
	assert.NotNil(t, client.Clientset, "clientset must still be built")
}

func TestNew_StillBindsNamespaceWhenProvided(t *testing.T) {
	t.Parallel()
	kubeconfig := minimalKubeconfig(t)

	client, err := New(kubeconfig, "test", "abc-zeebe")
	require.NoError(t, err)
	require.NotNil(t, client)
	assert.Equal(t, "abc-zeebe", client.Namespace)
}
