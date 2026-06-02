package aws

import (
	"context"
	"testing"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failRun fails the test if Inventory shells the CLI: the reachable set is the
// configured accounts, so Inventory must never run a command.
func failRun(t *testing.T) cloud.RunFunc {
	return func(_ context.Context, argv []string) (cloud.CLIResult, error) {
		t.Fatalf("Inventory must not shell the CLI; got %v", argv)
		return cloud.CLIResult{}, nil
	}
}

// TestInventoryUsesConfiguredAccounts proves the reachable set is exactly the
// configured accounts, reported without ever calling organizations list-accounts.
func TestInventoryUsesConfiguredAccounts(t *testing.T) {
	p := providerWithAccounts(t, "prod-aws", []Account{
		{ID: "111111111111", RoleARN: "arn:aws:iam::111111111111:role/r"},
		{ID: "222222222222", RoleARN: "arn:aws:iam::222222222222:role/r"},
	})
	inv, err := p.Inventory(context.Background(), failRun(t))
	require.NoError(t, err)
	require.Len(t, inv.Scopes, 2)
	assert.Equal(t, cloud.Scope{ID: "111111111111", Name: "111111111111"}, inv.Scopes[0])
	assert.Equal(t, cloud.Scope{ID: "222222222222", Name: "222222222222"}, inv.Scopes[1])
}

// TestInventoryCarriesAccountTags proves the deployment's per-account tags
// surface on the inventory scopes, so list_inventory can hand the agent the
// labels it uses to judge which account an investigation belongs to.
func TestInventoryCarriesAccountTags(t *testing.T) {
	p := providerWithAccounts(t, "prod-aws", []Account{
		{ID: "111111111111", RoleARN: "arn:aws:iam::111111111111:role/r", Tags: []string{"prod", "payments"}},
		{ID: "222222222222", RoleARN: "arn:aws:iam::222222222222:role/r"},
	})
	inv, err := p.Inventory(context.Background(), failRun(t))
	require.NoError(t, err)
	require.Len(t, inv.Scopes, 2)
	assert.Equal(t, []string{"prod", "payments"}, inv.Scopes[0].Tags)
	assert.Empty(t, inv.Scopes[1].Tags)
	assert.Equal(t, []string{"prod", "payments"}, p.ConfiguredTargets()[0].Tags)
}

// TestInventorySingleAccountIsOneEntry pins that a single-account source is just
// a one-entry list — the same code path as multi, with one scope.
func TestInventorySingleAccountIsOneEntry(t *testing.T) {
	p := providerWithAccounts(t, "prod-aws", []Account{
		{ID: "111111111111", RoleARN: "arn:aws:iam::111111111111:role/r"},
	})
	inv, err := p.Inventory(context.Background(), failRun(t))
	require.NoError(t, err)
	require.Len(t, inv.Scopes, 1)
	assert.Equal(t, cloud.Scope{ID: "111111111111", Name: "111111111111"}, inv.Scopes[0])
}
