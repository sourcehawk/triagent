package cloud

import (
	"context"
	"errors"
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

func (p *envProbeProvider) Identity(ctx context.Context, run RunFunc) (IdentityStatus, error) {
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
	st, err := Probe(context.Background(), p)
	require.NoError(t, err)
	assert.True(t, st.Valid)
	assert.Equal(t, "ro-sa@proj.iam.gserviceaccount.com", st.AssumedIdentity)
}

func TestProbeSurfacesProviderErrorAsInvalid(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{name: "aws", identityErr: errors.New("token expired")}
	st, err := Probe(context.Background(), p)
	require.NoError(t, err, "Probe should degrade, not error")
	assert.False(t, st.Valid, "expected Valid=false when the provider errors")
	assert.Equal(t, "aws", st.Provider, "expected provider name carried through")
	assert.NotEmpty(t, st.Hint, "expected the provider error surfaced as a hint")
}

// TestProbeUsesMinimalSubprocessEnv proves the probe path forwards only the
// base passthrough plus the provider's declared names to the whoami subprocess,
// dropping the launcher's ambient secrets. A parent canary must not cross the
// boundary while a declared passthrough var survives.
func TestProbeUsesMinimalSubprocessEnv(t *testing.T) {
	t.Setenv("TRIAGENT_CLOUD_LEAK_CANARY", "should-not-appear")
	t.Setenv("CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT", "ro-sa@proj.iam.gserviceaccount.com")
	p := &envProbeProvider{
		name:           "gcp",
		envPassthrough: []string{"CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT"},
	}

	st, err := Probe(context.Background(), p)
	require.NoError(t, err)

	seen := st.AssumedIdentity
	assert.NotContains(t, seen, "TRIAGENT_CLOUD_LEAK_CANARY",
		"parent-env secret must not reach the probe subprocess")
	assert.Contains(t, seen, "CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT=ro-sa@proj.iam.gserviceaccount.com",
		"declared passthrough var must reach the probe subprocess")
	for _, line := range strings.Split(seen, "\n") {
		if line == "" {
			continue
		}
		name, _, _ := strings.Cut(line, "=")
		assert.Contains(t, []string{"PATH", "HOME", "CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT"}, name,
			"only base + declared passthrough names may cross the boundary")
	}
}

func TestProbeInvalidWhenIdentityEmpty(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{name: "gcp", identity: IdentityStatus{Provider: "gcp", Valid: true}}
	st, err := Probe(context.Background(), p)
	require.NoError(t, err)
	assert.False(t, st.Valid, "an empty resolved identity must not be reported valid")
}
