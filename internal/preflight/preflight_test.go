package preflight

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/sourcehawk/triagent/pkg/auth"
)

// fakeProvider lets the preflight gate be tested without a real tsh session.
// loginErr exercises the auto-login fallback: when set, EnsureAuthenticated
// returns it (simulating e.g. operator cancelling SSO). Zero value means
// EnsureAuthenticated succeeds — equivalent to a real tsh flow completing.
type fakeProvider struct {
	authenticated bool
	advice        string
	loginErr      error
}

func (f fakeProvider) ListClusters(ctx context.Context) ([]auth.Cluster, error) {
	return nil, nil
}
func (f fakeProvider) Login(ctx context.Context, name string) (*auth.LoginResult, error) {
	return &auth.LoginResult{ClusterName: name, ContextName: name}, nil
}
func (f fakeProvider) IsAuthenticated() bool                          { return f.authenticated }
func (f fakeProvider) ReauthAdvice() string                           { return f.advice }
func (f fakeProvider) EnsureAuthenticated(ctx context.Context) error { return f.loginErr }

// When the session is missing, preflight auto-triggers tsh login. If
// that login itself fails (e.g. the operator cancelled SSO), the
// returned error must point them at the manual recovery command so the
// dead-end is recoverable from a terminal.
func TestRun_AuthFallbackLoginFailure(t *testing.T) {
	t.Parallel()
	_, err := Run(Options{
		Provider: fakeProvider{
			authenticated: false,
			advice:        "Teleport session expired or missing — run `tsh login --proxy=example.teleport.sh --auth=okta` in your terminal, then retry",
			loginErr:      errors.New("user cancelled"),
		},
		SessionDir:    t.TempDir(),
		MCPBinaryPath: "/tmp/triagent-mcp",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tsh login")
	assert.Contains(t, err.Error(), "user cancelled")
}

func TestRun_AuthenticatedHappyPath(t *testing.T) {
	t.Parallel()
	res, err := Run(Options{
		Provider:      fakeProvider{authenticated: true},
		SessionDir:    t.TempDir(),
		MCPBinaryPath: "/tmp/triagent-mcp",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, res.MCPConfigPath)
}

// writeKubeconfigFixture lays down a minimal but realistic kubeconfig at
// path with the given current-context and a couple of named contexts, so
// tests can observe (a) what got copied into the session-private path and
// (b) whether the operator's own file got mutated.
func writeKubeconfigFixture(t *testing.T, path, currentContext string) {
	t.Helper()
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["dev-cluster"] = &clientcmdapi.Cluster{Server: "https://dev.example:6443"}
	cfg.Clusters["prod-cluster"] = &clientcmdapi.Cluster{Server: "https://prod.example:6443"}
	cfg.AuthInfos["dev-user"] = &clientcmdapi.AuthInfo{Token: "dev-token"}
	cfg.AuthInfos["prod-user"] = &clientcmdapi.AuthInfo{Token: "prod-token"}
	cfg.Contexts["dev"] = &clientcmdapi.Context{Cluster: "dev-cluster", AuthInfo: "dev-user"}
	cfg.Contexts["prod"] = &clientcmdapi.Context{Cluster: "prod-cluster", AuthInfo: "prod-user"}
	cfg.CurrentContext = currentContext
	require.NoError(t, clientcmd.WriteToFile(*cfg, path))
}

// TestRun_FreezesKubeconfigToSessionDir is the regression guard for the
// operator-kubeconfig-leak bug: agent-side MCPs (notably triagent-teleport
// login) load kubeconfig via NewDefaultClientConfigLoadingRules() which
// honors $KUBECONFIG, then write back to GetDefaultFilename(). If preflight
// just pins $KUBECONFIG to the operator's real ~/.kube/config, those writes
// leak into the operator's shell (their `kubectl config current-context`
// flips behind their back). Preflight must instead freeze the kubeconfig
// into a session-private copy under SessionDir.
func TestRun_FreezesKubeconfigToSessionDir(t *testing.T) {
	sessionDir := t.TempDir()
	operatorHome := t.TempDir()
	source := filepath.Join(operatorHome, "kubeconfig.yaml")
	writeKubeconfigFixture(t, source, "dev")
	srcBefore, err := os.ReadFile(source)
	require.NoError(t, err)

	// Isolate from the test runner's own $KUBECONFIG / $HOME so preflight
	// only sees the fixture.
	t.Setenv("KUBECONFIG", source)
	t.Setenv("HOME", operatorHome)

	res, err := Run(Options{
		Ctx:           context.Background(),
		Provider:      fakeProvider{authenticated: true},
		SessionDir:    sessionDir,
		MCPBinaryPath: "/tmp/triagent-mcp",
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	// The frozen path must live under SessionDir, NOT the source.
	require.NotEqual(t, source, res.KubeconfigPath,
		"preflight must copy the operator's kubeconfig into SessionDir, not pin the source")
	rel, err := filepath.Rel(sessionDir, res.KubeconfigPath)
	require.NoError(t, err)
	require.False(t, len(rel) >= 2 && rel[:2] == "..",
		"frozen kubeconfig path %s must live under SessionDir %s", res.KubeconfigPath, sessionDir)

	// The frozen copy must carry the same contexts as the source so the
	// agent can switch_context without further setup.
	frozen, err := clientcmd.LoadFromFile(res.KubeconfigPath)
	require.NoError(t, err)
	assert.Contains(t, frozen.Contexts, "dev")
	assert.Contains(t, frozen.Contexts, "prod")

	// Simulate the leak: write through KUBECONFIG=<frozen path> using the
	// same default-loading-rules path that triagent-teleport's
	// updateKubeconfig() uses. This must NOT mutate the source.
	t.Setenv("KUBECONFIG", res.KubeconfigPath)
	loading := clientcmd.NewDefaultClientConfigLoadingRules()
	mutated, err := loading.Load()
	require.NoError(t, err)
	mutated.CurrentContext = "prod"
	require.NoError(t, clientcmd.WriteToFile(*mutated, loading.GetDefaultFilename()))

	srcAfter, err := os.ReadFile(source)
	require.NoError(t, err)
	assert.Equal(t, string(srcBefore), string(srcAfter),
		"operator's source kubeconfig must be byte-identical after an agent-style write through the frozen path")

	// And the frozen copy did pick up the change.
	frozenAfter, err := clientcmd.LoadFromFile(res.KubeconfigPath)
	require.NoError(t, err)
	assert.Equal(t, "prod", frozenAfter.CurrentContext,
		"the write must land on the session-private copy")
}

// TestRun_RehydratePreservesFrozenKubeconfig verifies that when preflight
// is re-run with opts.KubeconfigPath already pointing at the session-
// private frozen path (the rehydrate case), it does NOT re-copy from the
// operator's source — that would wipe out any contexts the agent had
// already logged into during the prior launcher run.
func TestRun_RehydratePreservesFrozenKubeconfig(t *testing.T) {
	sessionDir := t.TempDir()
	operatorHome := t.TempDir()
	source := filepath.Join(operatorHome, "kubeconfig.yaml")
	writeKubeconfigFixture(t, source, "dev")
	t.Setenv("KUBECONFIG", source)
	t.Setenv("HOME", operatorHome)

	// First preflight: freeze.
	res1, err := Run(Options{
		Ctx:           context.Background(),
		Provider:      fakeProvider{authenticated: true},
		SessionDir:    sessionDir,
		MCPBinaryPath: "/tmp/triagent-mcp",
	})
	require.NoError(t, err)
	require.NotEmpty(t, res1.KubeconfigPath)

	// Simulate the agent having logged into a new context that lives only
	// in the frozen copy.
	t.Setenv("KUBECONFIG", res1.KubeconfigPath)
	loading := clientcmd.NewDefaultClientConfigLoadingRules()
	mutated, err := loading.Load()
	require.NoError(t, err)
	mutated.Contexts["agent-logged-in"] = &clientcmdapi.Context{Cluster: "dev-cluster", AuthInfo: "dev-user"}
	require.NoError(t, clientcmd.WriteToFile(*mutated, loading.GetDefaultFilename()))

	// Rehydrate: caller passes back the persisted frozen path.
	res2, err := Run(Options{
		Ctx:            context.Background(),
		Provider:       fakeProvider{authenticated: true},
		SessionDir:     sessionDir,
		MCPBinaryPath:  "/tmp/triagent-mcp",
		KubeconfigPath: res1.KubeconfigPath,
	})
	require.NoError(t, err)
	require.Equal(t, res1.KubeconfigPath, res2.KubeconfigPath)

	// The agent-added context must survive — i.e. the frozen file was
	// NOT overwritten from the operator's source on rehydrate.
	after, err := clientcmd.LoadFromFile(res2.KubeconfigPath)
	require.NoError(t, err)
	assert.Contains(t, after.Contexts, "agent-logged-in",
		"rehydrate must not clobber contexts the agent added in a prior run")
}

// TestRun_EmptyNamespaceAuthenticatedWritesConfig verifies that an authenticated
// session with no namespace still writes a valid MCP config. This is the
// regression guard for the scope-unknown startup path: the agent discovers
// its scope at runtime via list_namespaces, so an empty Namespace must not
// prevent a successful preflight.
func TestRun_EmptyNamespaceAuthenticatedWritesConfig(t *testing.T) {
	sessionDir := t.TempDir()
	mcpBin := filepath.Join(sessionDir, "triagent-mcp-stub")
	require.NoError(t, os.WriteFile(mcpBin, []byte("#!/bin/sh\n"), 0o755))

	res, err := Run(Options{
		Ctx:           context.Background(),
		Provider:      fakeProvider{authenticated: true},
		Namespace:     "", // scope-unknown session: no clusterId at startup
		SessionDir:    sessionDir,
		MCPBinaryPath: mcpBin,
	})
	require.NoError(t, err, "authenticated Run must accept empty namespace")
	require.NotNil(t, res)
	assert.NotEmpty(t, res.MCPConfigPath, "MCP config should still be written for scope-unknown sessions")
	_, statErr := os.Stat(res.MCPConfigPath)
	assert.NoError(t, statErr, "MCPConfigPath should reference an existing file")
}
