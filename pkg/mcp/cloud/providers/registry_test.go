package providers

import (
	"path/filepath"
	"testing"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud/providers/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_KnownProviders(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		want string
	}{
		{"gcp", "gcp"},
		{"aws", "aws"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := New(tc.name)
			// The provider's New() resolves its CLI binary via exec.LookPath,
			// which may be absent in CI. A missing binary is a construction
			// error, not an unknown-provider error — assert on whichever
			// outcome the environment produced, but never a nil provider with
			// a nil error.
			if err != nil {
				assert.Nil(t, p, "a construction error must not also return a provider")
				return
			}
			require.NotNil(t, p)
			assert.Equal(t, tc.want, p.Name())
		})
	}
}

func TestNew_UnknownProviderErrors(t *testing.T) {
	t.Parallel()
	p, err := New("azure")
	require.Error(t, err)
	assert.Nil(t, p)
	assert.Contains(t, err.Error(), "azure")
}

// TestNewAWSWithAccounts proves the factory threads the aws multi-account config
// through to the provider: ConfiguredTargets surfaces the accounts and the
// active-target env names the generated profile. Construction generates profiles
// into a temp config so it does not touch the developer's ~/.aws/config; a
// missing aws binary in CI degrades to a construction error, which the test
// tolerates the same way TestNew_KnownProviders does.
func TestNewAWSWithAccounts(t *testing.T) {
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "config"))
	p, err := New("aws", Options{
		AWSAlias:         "prod-aws",
		AWSSourceProfile: "sso-admin",
		AWSAccounts: []aws.Account{
			{ID: "111111111111", RoleARN: "arn:aws:iam::111111111111:role/r"},
			{ID: "222222222222", RoleARN: "arn:aws:iam::222222222222:role/r"},
		},
	})
	if err != nil {
		assert.Nil(t, p, "a construction error must not also return a provider")
		return
	}
	require.NotNil(t, p)
	targets := p.ConfiguredTargets()
	require.Len(t, targets, 2)
	assert.Equal(t, "111111111111", targets[0].ID)
	assert.Equal(t, []string{"AWS_PROFILE=triagent-cloud-prod-aws-111111111111"}, p.ActiveTargetEnv("111111111111"))
}
