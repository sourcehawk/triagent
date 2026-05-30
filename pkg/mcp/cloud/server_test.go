package cloud

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRequiresProvider(t *testing.T) {
	t.Parallel()
	_, err := New(Options{})
	require.Error(t, err, "expected error when Provider is nil")
	_, err = New(Options{Provider: &fakeProvider{}})
	require.NoError(t, err)
}

// TestSubprocessEnvDropsParentSecretsKeepsPassthrough exercises the env the
// server actually builds for run_cli — the path the real harness takes, which
// the isolated execCLI test cannot cover. A parent-env canary must be dropped
// while a declared passthrough var survives, so ambient launcher secrets never
// reach the provider CLI.
func TestSubprocessEnvDropsParentSecretsKeepsPassthrough(t *testing.T) {
	t.Setenv("TRIAGENT_CLOUD_LEAK_CANARY", "should-not-appear")
	t.Setenv("CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT", "ro-sa@proj.iam.gserviceaccount.com")
	p := &fakeProvider{
		envPassthrough: []string{"CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT"},
	}
	srv := newTestServer(t, p)

	env := srv.subprocessEnv()

	assert.NotContains(t, env, "TRIAGENT_CLOUD_LEAK_CANARY=should-not-appear",
		"parent-env secret must be dropped from the subprocess env")
	assert.Contains(t, env, "CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT=ro-sa@proj.iam.gserviceaccount.com",
		"declared passthrough var must be forwarded")
}
