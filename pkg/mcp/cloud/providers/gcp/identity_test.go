package gcp

import (
	"context"
	"errors"
	"testing"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const targetSA = "ro-sa@proj.iam.gserviceaccount.com"

// fakeRun returns a canned CLIResult/error for a given argv, recording the
// argv it was called with so a test can assert the probe drove the right CLI.
type fakeRun struct {
	result cloud.CLIResult
	err    error
	calls  [][]string
}

func (f *fakeRun) run(_ context.Context, argv []string) (cloud.CLIResult, error) {
	f.calls = append(f.calls, argv)
	return f.result, f.err
}

func TestIdentityInvalidWhenNoImpersonationTargetPinned(t *testing.T) {
	t.Setenv(EnvImpersonate, targetSA)
	p, err := newWithBinary("/usr/bin/gcloud")
	require.NoError(t, err)

	f := &fakeRun{result: cloud.CLIResult{Stdout: `"token"`}}
	st, err := p.Identity(context.Background(), f.run, "")
	require.NoError(t, err)
	assert.False(t, st.Valid, "no pinned target means the session is not validly pinned")
	assert.NotEmpty(t, st.Hint)
	assert.Empty(t, f.calls, "no probe should run without a target")
}

func TestIdentityInvalidWhenImpersonationEnvNotPinnedToExpected(t *testing.T) {
	t.Setenv(EnvImpersonate, "someone-else@proj.iam.gserviceaccount.com")
	p, err := newWithBinary("/usr/bin/gcloud")
	require.NoError(t, err)

	f := &fakeRun{result: cloud.CLIResult{Stdout: `"token"`}}
	st, err := p.Identity(context.Background(), f.run, targetSA)
	require.NoError(t, err)
	assert.False(t, st.Valid, "impersonation env pinned to a different SA is invalid")
	assert.NotEmpty(t, st.Hint)
	assert.Empty(t, f.calls, "a mismatched pin short-circuits before the probe")
}

func TestIdentityValidWhenImpersonatedReadSucceeds(t *testing.T) {
	t.Setenv(EnvImpersonate, targetSA)
	p, err := newWithBinary("/usr/bin/gcloud")
	require.NoError(t, err)

	f := &fakeRun{result: cloud.CLIResult{Stdout: `"ya29.token"`}}
	st, err := p.Identity(context.Background(), f.run, targetSA)
	require.NoError(t, err)
	assert.Equal(t, "gcp", st.Provider)
	assert.Equal(t, targetSA, st.AssumedIdentity, "the SA is the identity the session acts as")
	assert.True(t, st.Valid, "a successful impersonated read proves the pin took effect")
	require.Len(t, f.calls, 1)
	assert.Equal(t, []string{"auth", "print-access-token", "--format=json"}, f.calls[0])
}

func TestIdentityInvalidWhenImpersonatedReadFails(t *testing.T) {
	t.Setenv(EnvImpersonate, targetSA)
	p, err := newWithBinary("/usr/bin/gcloud")
	require.NoError(t, err)

	f := &fakeRun{result: cloud.CLIResult{
		ExitCode: 1,
		Stderr:   "ERROR: Permission 'iam.serviceAccounts.getAccessToken' denied",
	}}
	st, err := p.Identity(context.Background(), f.run, targetSA)
	require.NoError(t, err)
	assert.False(t, st.Valid, "a failed impersonated read means the pin did not take effect")
	assert.Contains(t, st.Hint, "iam.serviceAccounts.getAccessToken",
		"the hint surfaces the captured stderr")
}

func TestIdentitySurfacesRunErrorAsHint(t *testing.T) {
	t.Setenv(EnvImpersonate, targetSA)
	p, err := newWithBinary("/usr/bin/gcloud")
	require.NoError(t, err)

	f := &fakeRun{err: errors.New("gcloud not authenticated")}
	st, err := p.Identity(context.Background(), f.run, targetSA)
	require.NoError(t, err, "a degraded auth state surfaces through Valid/Hint, not a Go error")
	assert.False(t, st.Valid)
	assert.Contains(t, st.Hint, "gcloud not authenticated")
}
