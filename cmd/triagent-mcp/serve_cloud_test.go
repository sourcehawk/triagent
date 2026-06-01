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
	assert.Empty(t, scope.Regions)
	assert.Empty(t, scope.Accounts)
}

func TestParseCloudScope_ValidJSON(t *testing.T) {
	t.Parallel()
	scope, err := parseCloudScope(`{"regions":["us-central1"],"accounts":["123456789012"]}`)
	require.NoError(t, err)
	assert.Equal(t, []string{"us-central1"}, scope.Regions)
	assert.Equal(t, []string{"123456789012"}, scope.Accounts)
}

func TestParseGCPProjects_EmptyYieldsNil(t *testing.T) {
	t.Parallel()
	got, err := parseGCPProjects("")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestParseGCPProjects_DecodesIDsAndTags(t *testing.T) {
	t.Parallel()
	got, err := parseGCPProjects(`[{"id":"prod-a","tags":["prod","payments"]},{"id":"prod-b"}]`)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "prod-a", got[0].ID)
	assert.Equal(t, []string{"prod", "payments"}, got[0].Tags)
	assert.Equal(t, "prod-b", got[1].ID)
	assert.Empty(t, got[1].Tags)
}

func TestParseGCPProjects_MalformedFailsClosed(t *testing.T) {
	t.Parallel()
	_, err := parseGCPProjects(`[{"id":`)
	require.Error(t, err)
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

func TestParseAWSAccounts_EmptyYieldsNil(t *testing.T) {
	t.Parallel()
	accs, err := parseAWSAccounts("")
	require.NoError(t, err)
	assert.Nil(t, accs)
}

func TestParseAWSAccounts_DecodesJSON(t *testing.T) {
	t.Parallel()
	accs, err := parseAWSAccounts(`[{"account_id":"111111111111","role_arn":"arn:aws:iam::111111111111:role/r"},{"account_id":"222222222222","role_arn":"arn:aws:iam::222222222222:role/r"}]`)
	require.NoError(t, err)
	require.Len(t, accs, 2)
	assert.Equal(t, "111111111111", accs[0].ID)
	assert.Equal(t, "arn:aws:iam::222222222222:role/r", accs[1].RoleARN)
}

func TestParseAWSAccounts_MalformedFailsClosed(t *testing.T) {
	t.Parallel()
	_, err := parseAWSAccounts(`[{"account_id":`)
	require.Error(t, err, "a malformed accounts list must fail closed, not silently drop accounts")
}

func TestRunCloud_MalformedAWSAccountsAborts(t *testing.T) {
	t.Setenv("TRIAGENT_CLOUD_PROVIDER", "aws")
	t.Setenv("TRIAGENT_CLOUD_AWS_ACCOUNTS", `[{"account_id":`)
	err := runCloud(context.Background(), serveFlags{kind: "cloud", cloudProvider: "aws"})
	require.Error(t, err, "a malformed accounts list must abort cloud-server startup")
	assert.Contains(t, err.Error(), "accounts", "the error should name the accounts")
}
