package providers

import (
	"context"
	"os"
	"testing"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud/providers/aws"
	"github.com/sourcehawk/triagent/pkg/mcp/cloud/providers/gcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProbeSourceDoesNotMutateProcessEnv pins the core guarantee of the
// explicit-threading refactor: ProbeSource builds the credential env for the
// subprocess without writing it into the launcher's own process env. A sentinel
// and the per-provider credential names must read identically before and after.
func TestProbeSourceDoesNotMutateProcessEnv(t *testing.T) {
	t.Setenv("TRIAGENT_PROBE_SENTINEL", "untouched")
	t.Setenv(aws.EnvProfile, "operator-base")
	if err := os.Unsetenv(gcp.EnvImpersonate); err != nil {
		require.NoError(t, err)
	}

	for _, src := range []Source{
		{Provider: "gcp", AssumedIdentity: "ro-sa@proj.iam.gserviceaccount.com"},
		{Provider: "aws", AssumedIdentity: "arn:aws:iam::111122223333:role/triage-ro", Profile: "triage-ro"},
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
