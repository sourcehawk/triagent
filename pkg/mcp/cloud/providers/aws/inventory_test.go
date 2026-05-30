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
			"sts get-caller-identity": {Stdout: callerIdentityAssumedRole},
		},
		errs: map[string]error{
			"organizations list-accounts": errAccessDenied,
		},
	}
	p, err := newWithBinary("/usr/bin/aws")
	require.NoError(t, err)

	inv, err := p.Inventory(context.Background(), f.run)
	require.NoError(t, err)

	require.Len(t, inv.Scopes, 1, "no orgs access falls back to the single caller account")
	assert.Equal(t, "111122223333", inv.Scopes[0].ID)
}

func TestInventoryFallsBackOnAccessDeniedExitCode(t *testing.T) {
	f := &fakeRun{
		results: map[string]cloud.CLIResult{
			"organizations list-accounts": {ExitCode: 254, Stdout: "An error occurred (AccessDeniedException) when calling the ListAccounts operation: ..."},
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
