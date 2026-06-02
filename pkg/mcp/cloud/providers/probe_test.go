package providers

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
	"github.com/sourcehawk/triagent/pkg/mcp/cloud/providers/aws"
	"github.com/sourcehawk/triagent/pkg/mcp/cloud/providers/gcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// blockingProvider's Identity blocks until the probe context is cancelled, then
// surfaces the context error the way a real provider does when its CLI is
// killed by the deadline. It lets the timeout be observed without a real sleep.
type blockingProvider struct{}

func (blockingProvider) Name() string                              { return "gcp" }
func (blockingProvider) Binary() string                            { return "/bin/true" }
func (blockingProvider) DefaultAllowlist() *cloud.CommandAllowlist { return &cloud.CommandAllowlist{} }
func (blockingProvider) DenyFloorAdditions() cloud.DenyFloor       { return cloud.DenyFloor{} }
func (blockingProvider) EnvPassthrough() []string                  { return nil }
func (blockingProvider) Inventory(context.Context, cloud.RunFunc) (cloud.Inventory, error) {
	return cloud.Inventory{}, nil
}
func (blockingProvider) ConfiguredTargets() []cloud.Target   { return nil }
func (blockingProvider) ActiveTargetEnv(string) []string     { return nil }
func (blockingProvider) ExpectedIdentity(string) (string, bool) { return "", false }

func (blockingProvider) Identity(ctx context.Context, _ cloud.RunFunc, _ string) (cloud.IdentityStatus, error) {
	<-ctx.Done()
	return cloud.IdentityStatus{}, ctx.Err()
}

func TestProbeProviderBoundsHungCLI(t *testing.T) {
	defer func(orig time.Duration) { probeTimeout = orig }(probeTimeout)
	probeTimeout = 50 * time.Millisecond

	done := make(chan cloud.IdentityStatus, 1)
	go func() {
		done <- probeProvider(context.Background(), blockingProvider{}, "", nil)
	}()

	select {
	case st := <-done:
		assert.False(t, st.Valid, "a hung probe must degrade to an invalid status, not block")
		assert.Equal(t, "gcp", st.Provider)
		assert.NotEmpty(t, st.Hint, "the deadline error must surface as a hint")
	case <-time.After(2 * time.Second):
		t.Fatal("probeProvider did not return: the probe timeout was not propagated")
	}
}

// TestProbeSourceDoesNotMutateProcessEnv pins the core guarantee of the
// explicit-threading refactor: ProbeSource builds the credential env for the
// subprocess without writing it into the launcher's own process env. A sentinel
// and the per-provider credential names must read identically before and after.
func TestProbeSourceDoesNotMutateProcessEnv(t *testing.T) {
	t.Setenv("TRIAGENT_PROBE_SENTINEL", "untouched")
	t.Setenv(aws.EnvProfile, "operator-base")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "config"))
	if err := os.Unsetenv(gcp.EnvImpersonate); err != nil {
		require.NoError(t, err)
	}

	for _, src := range []Source{
		{Provider: "gcp", AssumedIdentity: "ro-sa@proj.iam.gserviceaccount.com"},
		{Provider: "aws", Alias: "probe-aws", SourceProfile: "triage-ro", Accounts: []aws.Account{{ID: "111122223333", RoleARN: "arn:aws:iam::111122223333:role/triage-ro"}}},
	} {
		_ = ProbeSource(context.Background(), src)
	}

	assert.Equal(t, "untouched", os.Getenv("TRIAGENT_PROBE_SENTINEL"),
		"ProbeSource must not write to the process env")
	assert.Equal(t, "operator-base", os.Getenv(aws.EnvProfile),
		"ProbeSource must not pin AWS_PROFILE in the process env")
	_, set := os.LookupEnv(gcp.EnvImpersonate)
	assert.False(t, set, "ProbeSource must not pin the gcp impersonation env in the process env")
}

func TestProbeSourceUnknownProviderDegrades(t *testing.T) {
	st := ProbeSource(context.Background(), Source{Provider: "azure"})
	assert.False(t, st.Valid)
	assert.Equal(t, "azure", st.Provider)
	assert.NotEmpty(t, st.Hint)
}

// TestProbeSourceConstructionFailureKeepsPinnedIdentity proves a provider
// construction failure (here an unknown provider, which never reaches New's CLI
// lookup but exercises the same construction-error path) still reports the
// pinned identity, so preflight and connections name the degraded source's
// identity the operator must fix instead of an empty one.
func TestProbeSourceConstructionFailureKeepsPinnedIdentity(t *testing.T) {
	const pinned = "arn:aws:iam::111122223333:role/triage-ro"
	st := ProbeSource(context.Background(), Source{Provider: "azure", AssumedIdentity: pinned})
	assert.False(t, st.Valid)
	assert.Equal(t, "azure", st.Provider)
	assert.Equal(t, pinned, st.AssumedIdentity,
		"a construction failure must still carry the pinned identity")
	assert.NotEmpty(t, st.Hint)
}

// TestCredentialEnvAWSTargetsDefaultProfile proves the launcher-side probe for
// an aws source pins AWS_PROFILE to the default (first) account's generated
// profile name. The panel shows that default target's validity; per-account live
// validity is enforced by session_status on switch.
func TestCredentialEnvAWSTargetsDefaultProfile(t *testing.T) {
	env := credentialEnv(Source{
		Provider:      "aws",
		Alias:         "prod-aws",
		SourceProfile: "sso-admin",
		Accounts: []aws.Account{
			{ID: "111111111111", RoleARN: "arn:aws:iam::111111111111:role/r"},
			{ID: "222222222222", RoleARN: "arn:aws:iam::222222222222:role/r"},
		},
	})
	assert.Equal(t, "triagent-cloud-prod-aws-111111111111", env[aws.EnvProfile],
		"the probe must target the default account's generated profile")
}

// fakePassthroughProvider exposes a fixed EnvPassthrough so sourceEnv's
// carry-and-overlay behaviour can be asserted without a real cloud CLI.
type fakePassthroughProvider struct{ passthrough []string }

func (p *fakePassthroughProvider) EnvPassthrough() []string { return p.passthrough }

func TestSourceEnvOverlaysCredentialOverProcessEnv(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("CLOUDSDK_CONFIG", "/home/op/.config/gcloud")
	t.Setenv("TRIAGENT_PROBE_LEAK", "should-not-cross")
	t.Setenv(gcp.EnvImpersonate, "operator-leaked@proj.iam.gserviceaccount.com")

	p := &fakePassthroughProvider{passthrough: []string{gcp.EnvImpersonate, "CLOUDSDK_CONFIG"}}
	env := sourceEnvFor(p, Source{Provider: "gcp", AssumedIdentity: "ro-sa@proj.iam.gserviceaccount.com"})

	assert.Contains(t, env, "PATH=/usr/bin", "base PATH is carried from the process env")
	assert.Contains(t, env, "CLOUDSDK_CONFIG=/home/op/.config/gcloud", "declared config dir is carried")
	assert.Contains(t, env, gcp.EnvImpersonate+"=ro-sa@proj.iam.gserviceaccount.com",
		"the source credential overrides the process-env value")
	assert.NotContains(t, env, gcp.EnvImpersonate+"=operator-leaked@proj.iam.gserviceaccount.com",
		"the operator's ambient impersonation value must not survive the overlay")
	for _, kv := range env {
		assert.NotContains(t, kv, "TRIAGENT_PROBE_LEAK", "undeclared process env must not cross the boundary")
	}
}
