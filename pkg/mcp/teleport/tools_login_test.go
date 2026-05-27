package teleport

import (
	"context"
	"errors"
	"testing"

	"github.com/sourcehawk/triagent/pkg/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogin_HappyPath(t *testing.T) {
	t.Parallel()
	kubePath := kubeconfigWithContexts(t)
	provider := &fakeProvider{
		authenticated: true,
		loginResult:   &auth.LoginResult{ContextName: "teleport.example.io-prod-eu-1"},
	}
	srv := newTestServer(t, kubePath, provider)

	_, out, err := srv.login(context.Background(), nil, LoginInput{Cluster: "prod-eu-1"})
	require.NoError(t, err)
	assert.Equal(t, "teleport.example.io-prod-eu-1", out.ContextName)
	assert.Equal(t, kubePath, out.KubeconfigPath)
	assert.Equal(t, []string{"prod-eu-1"}, provider.loginCalls)
}

func TestLogin_AuthRequired(t *testing.T) {
	t.Parallel()
	provider := &fakeProvider{authenticated: false}
	srv := newTestServer(t, kubeconfigWithContexts(t), provider)

	res, _, err := srv.login(context.Background(), nil, LoginInput{Cluster: "prod-eu-1"})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.True(t, res.IsError)
	require.NotEmpty(t, res.Content)
	tc := res.Content[0].(*mcpTextContent)
	assert.Contains(t, tc.Text, "tsh login")
	assert.Empty(t, provider.loginCalls, "provider.Login must not be called when unauthenticated")
}

func TestLogin_ClusterRequired(t *testing.T) {
	t.Parallel()
	provider := &fakeProvider{authenticated: true}
	srv := newTestServer(t, kubeconfigWithContexts(t), provider)

	res, _, err := srv.login(context.Background(), nil, LoginInput{Cluster: ""})
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, res.Content[0].(*mcpTextContent).Text, "cluster is required")
}

func TestLogin_ProviderError(t *testing.T) {
	t.Parallel()
	provider := &fakeProvider{
		authenticated: true,
		loginErr:      errors.New("kube agent unreachable"),
	}
	srv := newTestServer(t, kubeconfigWithContexts(t), provider)

	res, _, err := srv.login(context.Background(), nil, LoginInput{Cluster: "prod-eu-1"})
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, res.Content[0].(*mcpTextContent).Text, "kube agent unreachable")
}
