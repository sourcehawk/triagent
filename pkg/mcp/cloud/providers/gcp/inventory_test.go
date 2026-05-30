package gcp

import (
	"context"
	"errors"
	"testing"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// projectsListJSON is captured `gcloud projects list --format=json` output.
const projectsListJSON = `[
  {
    "projectId": "triage-prod",
    "name": "Triage Production",
    "projectNumber": "111111111111",
    "lifecycleState": "ACTIVE"
  },
  {
    "projectId": "triage-staging",
    "name": "Triage Staging",
    "projectNumber": "222222222222",
    "lifecycleState": "ACTIVE"
  }
]`

func runReturning(out string) cloud.RunFunc {
	return func(context.Context, []string) (cloud.CLIResult, error) {
		return cloud.CLIResult{Stdout: out}, nil
	}
}

func TestInventoryProjectsIDAndName(t *testing.T) {
	t.Parallel()
	p, err := newWithBinary("/usr/bin/gcloud")
	require.NoError(t, err)

	inv, err := p.Inventory(context.Background(), runReturning(projectsListJSON))
	require.NoError(t, err)
	require.Len(t, inv.Scopes, 2)
	assert.Equal(t, cloud.Scope{ID: "triage-prod", Name: "Triage Production"}, inv.Scopes[0])
	assert.Equal(t, cloud.Scope{ID: "triage-staging", Name: "Triage Staging"}, inv.Scopes[1])
}

func TestInventoryEmptyWhenNoProjects(t *testing.T) {
	t.Parallel()
	p, err := newWithBinary("/usr/bin/gcloud")
	require.NoError(t, err)

	inv, err := p.Inventory(context.Background(), runReturning(`[]`))
	require.NoError(t, err)
	assert.Empty(t, inv.Scopes)
}

func TestInventoryCallsProjectsListWithJSONFormat(t *testing.T) {
	t.Parallel()
	p, err := newWithBinary("/usr/bin/gcloud")
	require.NoError(t, err)

	var gotArgv []string
	capturing := cloud.RunFunc(func(_ context.Context, argv []string) (cloud.CLIResult, error) {
		gotArgv = argv
		return cloud.CLIResult{Stdout: projectsListJSON}, nil
	})
	_, err = p.Inventory(context.Background(), capturing)
	require.NoError(t, err)
	assert.Equal(t, []string{"projects", "list", "--format=json"}, gotArgv,
		"the inventory argv must match the allowlisted `projects list` verb chain exactly")
}

func TestInventoryErrorsWhenRunErrors(t *testing.T) {
	t.Parallel()
	p, err := newWithBinary("/usr/bin/gcloud")
	require.NoError(t, err)

	failing := cloud.RunFunc(func(context.Context, []string) (cloud.CLIResult, error) {
		return cloud.CLIResult{}, errors.New("projects list rejected")
	})
	_, err = p.Inventory(context.Background(), failing)
	require.Error(t, err, "a run error is a real failure of the inventory tool, surfaced to the caller")
}
