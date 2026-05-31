package cloud

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// envProbeProvider drives the probe through a real subprocess: Binary is
// /usr/bin/env, which with no argv prints the environment it was handed. Its
// Identity runs that subprocess and reports the raw env back through
// IdentityStatus.AssumedIdentity, so a test can assert exactly which variables
// crossed the process boundary.
type envProbeProvider struct {
	name           string
	envPassthrough []string
}

func (p *envProbeProvider) Name() string                        { return p.name }
func (p *envProbeProvider) Binary() string                      { return "/usr/bin/env" }
func (p *envProbeProvider) DefaultAllowlist() *CommandAllowlist { return &CommandAllowlist{} }
func (p *envProbeProvider) DenyFloorAdditions() DenyFloor       { return DenyFloor{} }
func (p *envProbeProvider) EnvPassthrough() []string            { return p.envPassthrough }
func (p *envProbeProvider) Inventory(context.Context, RunFunc) (Inventory, error) {
	return Inventory{}, nil
}

func (p *envProbeProvider) ConfiguredTargets() []Target        { return nil }
func (p *envProbeProvider) ActiveTargetEnv(id string) []string { return []string{"FAKE_TARGET=" + id} }
func (p *envProbeProvider) Identity(ctx context.Context, run RunFunc, _ string) (IdentityStatus, error) {
	res, err := run(ctx, nil)
	if err != nil {
		return IdentityStatus{}, err
	}
	return IdentityStatus{
		Provider:        p.name,
		AssumedIdentity: res.Stdout,
		Valid:           true,
	}, nil
}

func TestProbeReturnsProviderIdentity(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{
		name: "gcp",
		identity: IdentityStatus{
			Provider:        "gcp",
			AssumedIdentity: "ro-sa@proj.iam.gserviceaccount.com",
			Valid:           true,
		},
	}
	st, err := Probe(context.Background(), p, "", nil)
	require.NoError(t, err)
	assert.True(t, st.Valid)
	assert.Equal(t, "ro-sa@proj.iam.gserviceaccount.com", st.AssumedIdentity)
}

func TestProbeErrorsOnNilProvider(t *testing.T) {
	t.Parallel()
	_, err := Probe(context.Background(), nil, "", nil)
	require.Error(t, err, "a nil provider is a caller contract violation, not a degrade")
}

func TestProbeSurfacesProviderErrorAsInvalid(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{name: "aws", identityErr: errors.New("token expired")}
	st, err := Probe(context.Background(), p, "", nil)
	require.NoError(t, err, "Probe should degrade, not error")
	assert.False(t, st.Valid, "expected Valid=false when the provider errors")
	assert.Equal(t, "aws", st.Provider, "expected provider name carried through")
	assert.NotEmpty(t, st.Hint, "expected the provider error surfaced as a hint")
}

// TestProbeExecsWithExactlyTheGivenEnv proves the probe execs the whoami
// subprocess under exactly the env the caller passed, with no read of
// os.Environ inside Probe: a parent canary set in the process env must not
// cross the boundary, while a var present only in the passed env survives.
func TestProbeExecsWithExactlyTheGivenEnv(t *testing.T) {
	t.Setenv("TRIAGENT_CLOUD_LEAK_CANARY", "should-not-appear")
	p := &envProbeProvider{name: "gcp"}

	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT=ro-sa@proj.iam.gserviceaccount.com",
	}
	st, err := Probe(context.Background(), p, "", env)
	require.NoError(t, err)

	seen := st.AssumedIdentity
	assert.NotContains(t, seen, "TRIAGENT_CLOUD_LEAK_CANARY",
		"a var present only in the process env, not the passed env, must not reach the subprocess")
	assert.Contains(t, seen, "CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT=ro-sa@proj.iam.gserviceaccount.com",
		"the passed env must reach the probe subprocess")
	for _, line := range strings.Split(seen, "\n") {
		if line == "" {
			continue
		}
		name, _, _ := strings.Cut(line, "=")
		assert.Contains(t, []string{"PATH", "CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT"}, name,
			"only the names in the passed env may cross the boundary")
	}
}

func TestProbeInvalidWhenIdentityEmpty(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{name: "gcp", identity: IdentityStatus{Provider: "gcp", Valid: true}}
	st, err := Probe(context.Background(), p, "", nil)
	require.NoError(t, err)
	assert.False(t, st.Valid, "an empty resolved identity must not be reported valid")
}

// TestProbeDegradedReportsPinnedIdentity proves a degraded probe still names
// WHICH pinned identity is degraded: when the provider errors and resolves no
// identity, Probe falls back to the expected identity the caller pinned, so
// session_status stays actionable instead of showing an empty identity.
func TestProbeDegradedReportsPinnedIdentity(t *testing.T) {
	t.Parallel()
	const pinned = "ro-sa@proj.iam.gserviceaccount.com"
	p := &fakeProvider{name: "gcp", identityErr: errors.New("token expired")}
	st, err := Probe(context.Background(), p, pinned, nil)
	require.NoError(t, err, "Probe should degrade, not error")
	assert.False(t, st.Valid)
	assert.Equal(t, pinned, st.AssumedIdentity,
		"a degraded probe must report the pinned identity so the operator knows what to fix")
}

// TestProbeFallsBackToExpectedWhenProviderOmitsIdentity covers the valid path:
// a provider that resolves to valid but reports no identity (an unusual but
// possible projection gap) still shows the pinned identity rather than empty.
func TestProbeFallsBackToExpectedWhenProviderOmitsIdentity(t *testing.T) {
	t.Parallel()
	const pinned = "arn:aws:iam::111122223333:role/triage-ro"
	p := &fakeProvider{name: "aws", identity: IdentityStatus{Provider: "aws", Valid: true}}
	st, err := Probe(context.Background(), p, pinned, nil)
	require.NoError(t, err)
	assert.Equal(t, pinned, st.AssumedIdentity,
		"an empty resolved identity must fall back to the pinned identity")
}
