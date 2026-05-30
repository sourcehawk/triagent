package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewResolvesProvider(t *testing.T) {
	p, err := New()
	require.NoError(t, err)
	require.NotNil(t, p)

	assert.Equal(t, "aws", p.Name())
	assert.NotEmpty(t, p.Binary(), "Binary should resolve to a non-empty path")
}

func TestDefaultAllowlistCoversReadOnlyAxes(t *testing.T) {
	p, err := New()
	require.NoError(t, err)

	allow := p.DefaultAllowlist()
	require.NotNil(t, allow)
	require.NotEmpty(t, allow.Commands)

	// The two commands Identity and Inventory shell through the validated run
	// core must be present, or those tools cannot work under the allowlist.
	assert.True(t, allow.Allows([]string{"sts", "get-caller-identity"}),
		"sts get-caller-identity must be allowlisted (identity + inventory fallback)")
	assert.True(t, allow.Allows([]string{"organizations", "list-accounts"}),
		"organizations list-accounts must be allowlisted (inventory primary)")

	// Spot-check coverage across the investigative axes.
	for _, argv := range [][]string{
		{"ec2", "describe-security-groups"},
		{"ec2", "describe-route-tables"},
		{"iam", "list-roles"},
		{"eks", "describe-cluster"},
		{"logs", "describe-log-groups"},
		{"cloudtrail", "lookup-events"},
	} {
		assert.Truef(t, allow.Allows(argv), "%v must be allowlisted", argv)
	}

	// Every entry must be a read-only verb and carry an axis description.
	for _, c := range allow.Commands {
		assert.NotEmpty(t, c.Description, "command %q must name its axis", c.Path)
	}
}

func TestDenyFloorAdditionsCoverCredentialReturningCommands(t *testing.T) {
	p, err := New()
	require.NoError(t, err)

	floor := p.DenyFloorAdditions()
	assert.Contains(t, floor.Subcommands, "ec2 get-password-data")
	assert.Contains(t, floor.Subcommands, "ec2-instance-connect send-ssh-public-key")
	assert.Contains(t, floor.Subcommands, "sts get-session-token")
	assert.Contains(t, floor.Subcommands, "sts get-federation-token")
}

func TestEnvPassthroughForwardsProfileAndRegionNames(t *testing.T) {
	p, err := New()
	require.NoError(t, err)

	got := p.EnvPassthrough()
	for _, name := range []string{
		"AWS_PROFILE",
		"AWS_REGION",
		"AWS_DEFAULT_REGION",
		"AWS_CONFIG_FILE",
		"AWS_SHARED_CREDENTIALS_FILE",
	} {
		assert.Contains(t, got, name)
	}
	// PATH/HOME are forwarded by the harness base set; the provider must not
	// duplicate them.
	assert.NotContains(t, got, "PATH")
	assert.NotContains(t, got, "HOME")
}

// fakeRun returns a canned CLIResult/error for a given argv, recording the argv
// it was called with so a test can assert the projection drove the right CLI.
type fakeRun struct {
	results map[string]cloud.CLIResult
	errs    map[string]error
	calls   [][]string
}

func (f *fakeRun) run(_ context.Context, argv []string) (cloud.CLIResult, error) {
	f.calls = append(f.calls, argv)
	key := keyOf(argv)
	if err, ok := f.errs[key]; ok {
		return cloud.CLIResult{}, err
	}
	return f.results[key], nil
}

func keyOf(argv []string) string {
	out := ""
	for _, a := range argv {
		if len(a) > 0 && a[0] == '-' {
			break
		}
		if out != "" {
			out += " "
		}
		out += a
	}
	return out
}

var errAccessDenied = errors.New("access denied (AccessDeniedException) when calling the ListAccounts operation")
