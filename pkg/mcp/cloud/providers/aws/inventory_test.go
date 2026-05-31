package aws

import (
	"context"
	"testing"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const listAccountsOutput = `{
  "Accounts": [
    { "Id": "111122223333", "Name": "prod", "Status": "ACTIVE" },
    { "Id": "444455556666", "Name": "staging", "Status": "ACTIVE" },
    { "Id": "777788889999", "Name": "suspended-acct", "Status": "SUSPENDED" }
  ]
}`

// TestInventoryUsesConfiguredAccounts proves a provider built with a configured
// accounts list reports exactly those accounts as the reachable set, without
// calling organizations list-accounts — the run func must never be invoked.
func TestInventoryUsesConfiguredAccounts(t *testing.T) {
	p := providerWithAccounts(t, "prod-aws", []Account{
		{ID: "111111111111", RoleARN: "arn:aws:iam::111111111111:role/r"},
		{ID: "222222222222", RoleARN: "arn:aws:iam::222222222222:role/r"},
	})
	failRun := func(_ context.Context, argv []string) (cloud.CLIResult, error) {
		t.Fatalf("Inventory must not shell the CLI when accounts are configured; got %v", argv)
		return cloud.CLIResult{}, nil
	}
	inv, err := p.Inventory(context.Background(), failRun)
	require.NoError(t, err)
	require.Len(t, inv.Scopes, 2)
	assert.Equal(t, cloud.Scope{ID: "111111111111", Name: "111111111111"}, inv.Scopes[0])
	assert.Equal(t, cloud.Scope{ID: "222222222222", Name: "222222222222"}, inv.Scopes[1])
}

func TestInventoryProjectsActiveAccounts(t *testing.T) {
	f := &fakeRun{results: map[string]cloud.CLIResult{
		"organizations list-accounts": {Stdout: listAccountsOutput},
	}}
	p, err := newWithBinary("/usr/bin/aws")
	require.NoError(t, err)

	inv, err := p.Inventory(context.Background(), f.run)
	require.NoError(t, err)

	require.Len(t, f.calls, 1)
	assert.Equal(t, []string{"organizations", "list-accounts", "--output", "json"}, f.calls[0])

	require.Len(t, inv.Scopes, 2, "suspended accounts are dropped")
	assert.Equal(t, cloud.Scope{ID: "111122223333", Name: "prod"}, inv.Scopes[0])
	assert.Equal(t, cloud.Scope{ID: "444455556666", Name: "staging"}, inv.Scopes[1])
}

func TestInventoryFallsBackToCallerAccountOnAccessDenied(t *testing.T) {
	f := &fakeRun{
		results: map[string]cloud.CLIResult{
			"organizations list-accounts": {ExitCode: 254, Stderr: "An error occurred (AccessDeniedException) when calling the ListAccounts operation: ..."},
			"sts get-caller-identity":     {Stdout: callerIdentityAssumedRole},
		},
	}
	p, err := newWithBinary("/usr/bin/aws")
	require.NoError(t, err)

	inv, err := p.Inventory(context.Background(), f.run)
	require.NoError(t, err)

	require.Len(t, inv.Scopes, 1, "no orgs access falls back to the single caller account")
	assert.Equal(t, "111122223333", inv.Scopes[0].ID)
}

func TestInventoryFallsBackWhenOrgsNotInUse(t *testing.T) {
	f := &fakeRun{
		results: map[string]cloud.CLIResult{
			"organizations list-accounts": {ExitCode: 254, Stderr: "An error occurred (AWSOrganizationsNotInUseException) when calling the ListAccounts operation"},
			"sts get-caller-identity":     {Stdout: callerIdentityAssumedRole},
		},
	}
	p, err := newWithBinary("/usr/bin/aws")
	require.NoError(t, err)

	inv, err := p.Inventory(context.Background(), f.run)
	require.NoError(t, err)
	require.Len(t, inv.Scopes, 1)
	assert.Equal(t, "111122223333", inv.Scopes[0].ID)
}

func TestInventorySurfacesNonAccessDeniedFailure(t *testing.T) {
	f := &fakeRun{
		results: map[string]cloud.CLIResult{
			"organizations list-accounts": {ExitCode: 254, Stderr: "An error occurred (ThrottlingException) when calling the ListAccounts operation: Rate exceeded"},
			"sts get-caller-identity":     {Stdout: callerIdentityAssumedRole},
		},
	}
	p, err := newWithBinary("/usr/bin/aws")
	require.NoError(t, err)

	_, err = p.Inventory(context.Background(), f.run)
	require.Error(t, err, "a throttling failure must surface, not silently fall back")
	assert.Contains(t, err.Error(), "ThrottlingException", "the error surfaces the captured stderr")
	// The fallback caller-identity call must not have run.
	for _, c := range f.calls {
		assert.NotEqual(t, []string{"sts", "get-caller-identity", "--output", "json"}, c,
			"a non-AccessDenied failure must not trigger the single-account fallback")
	}
}

func TestInventorySurfacesTransportError(t *testing.T) {
	f := &fakeRun{
		errs: map[string]error{
			"organizations list-accounts": errAccessDenied,
		},
	}
	p, err := newWithBinary("/usr/bin/aws")
	require.NoError(t, err)

	_, err = p.Inventory(context.Background(), f.run)
	require.Error(t, err, "a process that could not be run is a transport failure, surfaced not masked")
}
