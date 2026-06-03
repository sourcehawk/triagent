package teleport

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sourcehawk/triagent/pkg/auth"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mcpTextContent = mcp.TextContent

// fakeProvider stubs TeleportProvider for tests.
type fakeProvider struct {
	authenticated bool
	clusters      []auth.Cluster
	clustersErr   error
	loginResult   *auth.LoginResult
	loginErr      error
	loginCalls    []string
	reauthAdvice  string
}

func (f *fakeProvider) IsAuthenticated() bool { return f.authenticated }
func (f *fakeProvider) ListClusters(_ context.Context) ([]auth.Cluster, error) {
	return f.clusters, f.clustersErr
}
func (f *fakeProvider) Login(_ context.Context, c string) (*auth.LoginResult, error) {
	f.loginCalls = append(f.loginCalls, c)
	return f.loginResult, f.loginErr
}
func (f *fakeProvider) ReauthAdvice() string {
	if f.reauthAdvice != "" {
		return f.reauthAdvice
	}
	return "Teleport session expired or missing — run `tsh login` in your terminal, then retry"
}

// kubeconfigWithContexts writes a minimal kubeconfig with the named contexts.
func kubeconfigWithContexts(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig")
	body := "apiVersion: v1\nkind: Config\ncurrent-context: \ncontexts:\n"
	for _, n := range names {
		body += "- name: " + n + "\n  context:\n    cluster: " + n + "-cluster\n    user: u\n"
	}
	body += "clusters: []\nusers: []\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func newTestServer(t *testing.T, kubePath string, provider *fakeProvider) *Server {
	t.Helper()
	srv, err := New(Options{KubeconfigPath: kubePath, Provider: provider})
	require.NoError(t, err)
	return srv
}

func TestListClusters_ReturnsAllClusters(t *testing.T) {
	t.Parallel()
	provider := &fakeProvider{
		authenticated: true,
		clusters: []auth.Cluster{
			{Name: "prod-eu-1", Labels: map[string]string{"env": "prod"}},
			{Name: "dev-eu-1", Labels: map[string]string{"env": "dev"}},
		},
	}
	srv := newTestServer(t, kubeconfigWithContexts(t), provider)

	_, out, err := srv.listClusters(context.Background(), nil, ListClustersInput{})
	require.NoError(t, err)
	require.Len(t, out.Items, 2)
	assert.Equal(t, "dev-eu-1", out.Items[0].Name, "results sorted by name")
	assert.Equal(t, "prod", out.Items[1].Labels["env"])
}

func TestListClusters_FilterSubstring(t *testing.T) {
	t.Parallel()
	provider := &fakeProvider{
		authenticated: true,
		clusters: []auth.Cluster{
			{Name: "prod-eu-1"},
			{Name: "prod-us-1"},
			{Name: "dev-eu-1"},
		},
	}
	srv := newTestServer(t, kubeconfigWithContexts(t), provider)

	_, out, err := srv.listClusters(context.Background(), nil, ListClustersInput{Filter: "prod"})
	require.NoError(t, err)
	require.Len(t, out.Items, 2)
	assert.Equal(t, "prod-eu-1", out.Items[0].Name)
	assert.Equal(t, "prod-us-1", out.Items[1].Name)
}

func TestListClusters_MarksCurrentlyLoggedIn(t *testing.T) {
	t.Parallel()
	provider := &fakeProvider{
		authenticated: true,
		clusters: []auth.Cluster{
			{Name: "prod-eu-1"},
			{Name: "dev-eu-1"},
		},
	}
	// kubeContextName(siteName, clusterName) formats as "<site>-<cluster>"; the
	// SDK's exact format is opaque, so we use a substring match in the
	// helper (see implementation). For this test we plant a context name
	// equal to the cluster name; the production matcher accepts that as
	// "logged in" because the context name contains the cluster name.
	srv := newTestServer(t, kubeconfigWithContexts(t, "prod-eu-1"), provider)

	_, out, err := srv.listClusters(context.Background(), nil, ListClustersInput{})
	require.NoError(t, err)
	require.Len(t, out.Items, 2)
	got := map[string]bool{}
	for _, c := range out.Items {
		got[c.Name] = c.CurrentlyLoggedIn
	}
	assert.True(t, got["prod-eu-1"], "prod-eu-1 should be marked logged in")
	assert.False(t, got["dev-eu-1"], "dev-eu-1 should not be marked logged in")
}

func TestListClusters_FilterMatchesLabelValue(t *testing.T) {
	t.Parallel()
	// The alert names the cluster in one shape (`prod-gke-europe-west1-worker-1`)
	// but Teleport's canonical name is different (`saas-prod-worker-1`). Teleport
	// typically tags clusters with their cloud-provider identifier; the filter
	// should match against labels too so the agent finds the cluster on the
	// first targeted call rather than falling back to the full listing.
	provider := &fakeProvider{
		authenticated: true,
		clusters: []auth.Cluster{
			{Name: "saas-prod-worker-1", Labels: map[string]string{"kubernetes-cluster": "prod-gke-europe-west1-worker-1"}},
			{Name: "saas-prod-worker-2", Labels: map[string]string{"kubernetes-cluster": "prod-gke-europe-west1-worker-2"}},
		},
	}
	srv := newTestServer(t, kubeconfigWithContexts(t), provider)

	_, out, err := srv.listClusters(context.Background(), nil, ListClustersInput{Filter: "prod-gke-europe-west1-worker-1"})
	require.NoError(t, err)
	require.Len(t, out.Items, 1, "filter should match via label value")
	assert.Equal(t, "saas-prod-worker-1", out.Items[0].Name)
}

func TestListClusters_ReturnsKubeContextWhenLoggedIn(t *testing.T) {
	t.Parallel()
	// Teleport's kubeconfig context name has a different shape from its
	// short cluster name (e.g. `camunda.teleport.sh-saas-prod-worker-1` vs
	// `saas-prod-worker-1`). When the agent reads CurrentlyLoggedIn=true
	// it needs the full context name to pass to switch_context. Return it.
	provider := &fakeProvider{
		authenticated: true,
		clusters: []auth.Cluster{
			{Name: "saas-prod-worker-1"},
			{Name: "saas-prod-worker-2"},
		},
	}
	srv := newTestServer(t, kubeconfigWithContexts(t, "camunda.teleport.sh-saas-prod-worker-1"), provider)

	_, out, err := srv.listClusters(context.Background(), nil, ListClustersInput{})
	require.NoError(t, err)
	require.Len(t, out.Items, 2)
	got := map[string]ClusterInfo{}
	for _, c := range out.Items {
		got[c.Name] = c
	}
	assert.True(t, got["saas-prod-worker-1"].CurrentlyLoggedIn)
	assert.Equal(t, "camunda.teleport.sh-saas-prod-worker-1", got["saas-prod-worker-1"].KubeContext)
	assert.False(t, got["saas-prod-worker-2"].CurrentlyLoggedIn)
	assert.Empty(t, got["saas-prod-worker-2"].KubeContext)
}

func TestListClusters_AuthRequired(t *testing.T) {
	t.Parallel()
	provider := &fakeProvider{authenticated: false}
	srv := newTestServer(t, kubeconfigWithContexts(t), provider)

	res, _, err := srv.listClusters(context.Background(), nil, ListClustersInput{})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.True(t, res.IsError, "expected error result")
	require.NotEmpty(t, res.Content)
	tc, ok := res.Content[0].(*mcpTextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "tsh login", "auth-failure message must name the command to run")
}

// The auth-required message must come from the provider so it reflects the
// deployment's configured proxy/connector — not a static string baked against
// the empty package defaults (which produced `tsh login --proxy= --auth=okta`).
func TestAuthRequiredMessage_ComesFromProvider(t *testing.T) {
	t.Parallel()
	provider := &fakeProvider{authenticated: false, reauthAdvice: "SENTINEL-REAUTH-ADVICE"}
	srv := newTestServer(t, kubeconfigWithContexts(t), provider)

	clusters, _, err := srv.listClusters(context.Background(), nil, ListClustersInput{})
	require.NoError(t, err)
	require.True(t, clusters.IsError)
	assert.Contains(t, clusters.Content[0].(*mcpTextContent).Text, "SENTINEL-REAUTH-ADVICE")

	login, _, err := srv.login(context.Background(), nil, LoginInput{Cluster: "prod-eu-1"})
	require.NoError(t, err)
	require.True(t, login.IsError)
	assert.Contains(t, login.Content[0].(*mcpTextContent).Text, "SENTINEL-REAUTH-ADVICE")
}

// Regression for the empty-proxy bug: a teleport deployment with a configured
// proxy must see that proxy in the re-auth advice. Constructs the real
// provider (no stub) with an empty TELEPORT_HOME so it reads as unauthenticated
// deterministically, then asserts the proxy threaded through New → provider →
// message.
func TestNew_ThreadsConfiguredProxyIntoAuthMessage(t *testing.T) {
	t.Setenv("TELEPORT_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	srv, err := New(Options{
		KubeconfigPath: kubeconfigWithContexts(t),
		Proxy:          "proxy.example.com",
		AuthConnector:  "okta",
	})
	require.NoError(t, err)

	res, _, err := srv.listClusters(context.Background(), nil, ListClustersInput{})
	require.NoError(t, err)
	require.True(t, res.IsError, "no Teleport session in test env, so auth must be required")
	text := res.Content[0].(*mcpTextContent).Text
	assert.Contains(t, text, "--proxy=proxy.example.com", "configured proxy must reach the message")
	assert.Contains(t, text, "--auth=okta")
}
