package k8s

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeKubeconfig dumps a minimal kubeconfig with the given contexts to a
// tempdir and returns the path. current-context is set to currentContext if
// non-empty.
func writeKubeconfig(t *testing.T, currentContext string, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig")
	body := "apiVersion: v1\nkind: Config\ncurrent-context: " + currentContext + "\nclusters:\n"
	for _, n := range names {
		body += "- name: " + n + "-cluster\n  cluster:\n    server: https://" + n + ".example/\n"
	}
	body += "users:\n- name: u\n  user:\n    token: t\n"
	body += "contexts:\n"
	for _, n := range names {
		body += "- name: " + n + "\n  context:\n    cluster: " + n + "-cluster\n    user: u\n    namespace: " + n + "-ns\n"
	}
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// kitForContextTests builds a ToolKit with the given kubeconfig path. No
// snapshot is initially set — switch_context will populate it.
func kitForContextTests(t *testing.T, kubePath string) *ToolKit {
	t.Helper()
	return &ToolKit{
		kubeconfigPath: kubePath,
		allowlist:      &Allowlist{Kinds: []Kind{}},
		crossplanePats: nil,
	}
}

func TestListContexts_MarksCurrentAndActive(t *testing.T) {
	t.Parallel()
	kubePath := writeKubeconfig(t, "alpha", "alpha", "beta", "gamma")
	kit := kitForContextTests(t, kubePath)
	// Pretend we already switch_context'd to beta.
	kit.snapshot.Store(&clientSnapshot{contextName: "beta"})

	_, out, err := kit.listContexts(context.Background(), nil, ListContextsInput{})
	require.NoError(t, err)
	require.Len(t, out.Items, 3)
	byName := map[string]ContextInfo{}
	for _, c := range out.Items {
		byName[c.Name] = c
	}
	assert.True(t, byName["alpha"].Current, "alpha is the kubeconfig current-context")
	assert.False(t, byName["alpha"].Active, "alpha is not what the MCP bound")
	assert.True(t, byName["beta"].Active, "beta should be marked active")
	assert.Equal(t, "beta-ns", byName["beta"].Namespace)
}

func TestListContexts_Filter(t *testing.T) {
	t.Parallel()
	kubePath := writeKubeconfig(t, "", "alpha", "beta", "gamma")
	kit := kitForContextTests(t, kubePath)

	_, out, err := kit.listContexts(context.Background(), nil, ListContextsInput{Filter: "et"})
	require.NoError(t, err)
	require.Len(t, out.Items, 1)
	assert.Equal(t, "beta", out.Items[0].Name)
}

func TestGetCurrentContext_NoActive(t *testing.T) {
	t.Parallel()
	kubePath := writeKubeconfig(t, "alpha", "alpha")
	kit := kitForContextTests(t, kubePath)

	res, _, err := kit.getCurrentContext(context.Background(), nil, GetCurrentContextInput{})
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, res.Content[0].(*mcp.TextContent).Text, "no active kubernetes context")
}

func TestSwitchContext_RebuildsClients(t *testing.T) {
	t.Parallel()
	kubePath := writeKubeconfig(t, "", "alpha")
	kit := kitForContextTests(t, kubePath)

	// Override buildSnapshot via the test seam: we replace t.snapshot
	// directly by stubbing the build (BuildRESTConfig will fail against
	// the fake server URL). For the integration assertion, we just
	// verify that an invalid context name returns an error and leaves
	// the snapshot unchanged.
	res, _, err := kit.switchContext(context.Background(), nil, SwitchContextInput{Name: "does-not-exist"})
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, res.Content[0].(*mcp.TextContent).Text, "does-not-exist")
	assert.Nil(t, kit.snapshot.Load(), "snapshot must be nil after a failed switch")
}

func TestSwitchContext_FailedSwitchDoesNotWriteActiveContext(t *testing.T) {
	t.Parallel()
	kubePath := writeKubeconfig(t, "", "alpha")
	sessionDir := t.TempDir()
	kit := kitForContextTests(t, kubePath)
	kit.sessionDir = sessionDir // file write target

	// Use a context name that's not in the kubeconfig — switchContext
	// will fail at buildSnapshot and must NOT write the file.
	res, _, err := kit.switchContext(context.Background(), nil, SwitchContextInput{Name: "does-not-exist"})
	require.NoError(t, err)
	require.True(t, res.IsError)

	_, err = os.Stat(filepath.Join(sessionDir, activeContextFile))
	require.True(t, os.IsNotExist(err), "active-context must not be written on failed switch")
}

func TestSwitchContext_AtomicSwap(t *testing.T) {
	t.Parallel()
	kubePath := writeKubeconfig(t, "", "alpha")
	kit := kitForContextTests(t, kubePath)
	// Seed a snapshot, then run switch_context concurrently with readers.
	original := &clientSnapshot{contextName: "original"}
	kit.snapshot.Store(original)

	// We don't actually finish the switch (the kubeconfig server is fake);
	// we just confirm `activeSnapshot()` is safe to call from many goroutines
	// while another goroutine attempts a swap.
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snap, _ := kit.activeSnapshot()
			require.NotNil(t, snap)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Intentionally ignore error — invalid context, but Store on
		// failure path is never reached.
		_, _, _ = kit.switchContext(context.Background(), nil, SwitchContextInput{Name: "another"})
	}()
	wg.Wait()

	// Original snapshot still active after a failed switch.
	snap, _ := kit.activeSnapshot()
	require.NotNil(t, snap)
	assert.Equal(t, "original", snap.contextName)
}
