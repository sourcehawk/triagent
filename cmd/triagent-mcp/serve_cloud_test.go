package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunServe_CloudKindRequiresProvider(t *testing.T) {
	t.Parallel()
	err := runServe(context.Background(), serveFlags{kind: "cloud"})
	require.Error(t, err, "expected error when --provider is missing")
	assert.Contains(t, err.Error(), "provider", "error should mention --provider")
}

func TestRunServe_CloudKindRejectsUnknownProvider(t *testing.T) {
	t.Parallel()
	err := runServe(context.Background(), serveFlags{kind: "cloud", cloudProvider: "azure"})
	require.Error(t, err, "expected error for an unknown provider")
	assert.Contains(t, err.Error(), "azure", "error should name the rejected provider")
}

func TestRunServe_UnknownKindErrorListsCloud(t *testing.T) {
	t.Parallel()
	err := runServe(context.Background(), serveFlags{kind: "bogus"})
	require.Error(t, err, "expected error for unknown kind")
	assert.Contains(t, err.Error(), "cloud", "kind list should include cloud")
}

func TestServeCmd_KnowsCloudKind(t *testing.T) {
	t.Parallel()
	cmd := serveCmd()
	assert.Contains(t, cmd.Long, "cloud", "serve --help should list cloud")
}

func TestParseCloudScope_EmptyYieldsUnconstrained(t *testing.T) {
	t.Parallel()
	scope, err := parseCloudScope("")
	require.NoError(t, err)
	assert.Empty(t, scope.Projects)
	assert.Empty(t, scope.Regions)
	assert.Empty(t, scope.Accounts)
}

func TestParseCloudScope_ValidJSON(t *testing.T) {
	t.Parallel()
	scope, err := parseCloudScope(`{"projects":["prod"],"regions":["us-central1"]}`)
	require.NoError(t, err)
	assert.Equal(t, []string{"prod"}, scope.Projects)
	assert.Equal(t, []string{"us-central1"}, scope.Regions)
}

func TestParseCloudScope_MalformedFailsClosed(t *testing.T) {
	t.Parallel()
	_, err := parseCloudScope(`{"projects":`)
	require.Error(t, err, "a malformed scope must fail closed, not silently drop restrictions")
}

func TestRunCloud_MalformedScopeAborts(t *testing.T) {
	t.Setenv("TRIAGENT_CLOUD_PROVIDER", "gcp")
	t.Setenv("TRIAGENT_CLOUD_SCOPE", `{"projects":`)
	err := runCloud(context.Background(), serveFlags{kind: "cloud", cloudProvider: "gcp"})
	require.Error(t, err, "a malformed scope must abort cloud-server startup")
	assert.Contains(t, err.Error(), "scope", "the error should name the scope")
}
