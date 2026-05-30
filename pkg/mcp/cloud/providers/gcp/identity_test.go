package gcp

import (
	"context"
	"errors"
	"testing"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// authListJSON is captured `gcloud auth list --format=json` output: an array of
// accounts, exactly one with status ACTIVE.
const authListJSON = `[
  {
    "account": "ro-sa@proj.iam.gserviceaccount.com",
    "status": "ACTIVE"
  },
  {
    "account": "operator@example.com",
    "status": ""
  }
]`

func runReturning(out string) cloud.RunFunc {
	return func(context.Context, []string) (cloud.CLIResult, error) {
		return cloud.CLIResult{Stdout: out}, nil
	}
}

func TestIdentityResolvesActiveAccountAsTarget(t *testing.T) {
	t.Setenv(impersonationEnv, "ro-sa@proj.iam.gserviceaccount.com")
	p, err := newWithBinary("/usr/bin/gcloud")
	require.NoError(t, err)

	st, err := p.Identity(context.Background(), runReturning(authListJSON))
	require.NoError(t, err)
	assert.Equal(t, "gcp", st.Provider)
	assert.Equal(t, "ro-sa@proj.iam.gserviceaccount.com", st.AssumedIdentity)
	assert.True(t, st.Valid, "active account equals the impersonation target")
}

func TestIdentityInvalidWhenActiveAccountIsNotTheTarget(t *testing.T) {
	t.Setenv(impersonationEnv, "ro-sa@proj.iam.gserviceaccount.com")
	p, err := newWithBinary("/usr/bin/gcloud")
	require.NoError(t, err)

	mismatch := `[{"account": "operator@example.com", "status": "ACTIVE"}]`
	st, err := p.Identity(context.Background(), runReturning(mismatch))
	require.NoError(t, err)
	assert.Equal(t, "operator@example.com", st.AssumedIdentity)
	assert.False(t, st.Valid, "active account differs from the impersonation target")
	assert.NotEmpty(t, st.Hint)
}

func TestIdentityInvalidWhenNoActiveAccount(t *testing.T) {
	t.Setenv(impersonationEnv, "ro-sa@proj.iam.gserviceaccount.com")
	p, err := newWithBinary("/usr/bin/gcloud")
	require.NoError(t, err)

	st, err := p.Identity(context.Background(), runReturning(`[]`))
	require.NoError(t, err)
	assert.Empty(t, st.AssumedIdentity)
	assert.False(t, st.Valid)
	assert.NotEmpty(t, st.Hint)
}

func TestIdentityInvalidWhenNoImpersonationTargetPinned(t *testing.T) {
	t.Setenv(impersonationEnv, "")
	p, err := newWithBinary("/usr/bin/gcloud")
	require.NoError(t, err)

	st, err := p.Identity(context.Background(), runReturning(authListJSON))
	require.NoError(t, err)
	assert.False(t, st.Valid, "no pinned target means the session is not validly pinned")
	assert.NotEmpty(t, st.Hint)
}

func TestIdentitySurfacesRunErrorAsHint(t *testing.T) {
	t.Setenv(impersonationEnv, "ro-sa@proj.iam.gserviceaccount.com")
	p, err := newWithBinary("/usr/bin/gcloud")
	require.NoError(t, err)

	failing := cloud.RunFunc(func(context.Context, []string) (cloud.CLIResult, error) {
		return cloud.CLIResult{}, errors.New("gcloud not authenticated")
	})
	st, err := p.Identity(context.Background(), failing)
	require.NoError(t, err, "a degraded auth state surfaces through Valid/Hint, not a Go error")
	assert.False(t, st.Valid)
	assert.Contains(t, st.Hint, "gcloud not authenticated")
}

func TestIdentityCallsAuthListWithJSONFormat(t *testing.T) {
	t.Setenv(impersonationEnv, "ro-sa@proj.iam.gserviceaccount.com")
	p, err := newWithBinary("/usr/bin/gcloud")
	require.NoError(t, err)

	var gotArgv []string
	capturing := cloud.RunFunc(func(_ context.Context, argv []string) (cloud.CLIResult, error) {
		gotArgv = argv
		return cloud.CLIResult{Stdout: authListJSON}, nil
	})
	_, err = p.Identity(context.Background(), capturing)
	require.NoError(t, err)
	assert.Equal(t, []string{"auth", "list", "--filter=status:ACTIVE", "--format=json"}, gotArgv)
}
