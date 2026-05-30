package aws

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewResolvesProvider(t *testing.T) {
	p, err := newWithBinary("/usr/bin/aws")
	require.NoError(t, err)
	require.NotNil(t, p)

	assert.Equal(t, "aws", p.Name())
	assert.Equal(t, "/usr/bin/aws", p.Binary())
}

func TestDefaultAllowlistCoversReadOnlyAxes(t *testing.T) {
	p, err := newWithBinary("/usr/bin/aws")
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
	p, err := newWithBinary("/usr/bin/aws")
	require.NoError(t, err)

	floor := p.DenyFloorAdditions()
	assert.Contains(t, floor.Subcommands, "ec2 get-password-data")
	assert.Contains(t, floor.Subcommands, "ec2-instance-connect send-ssh-public-key")
	assert.Contains(t, floor.Subcommands, "sts get-session-token")
	assert.Contains(t, floor.Subcommands, "sts get-federation-token")
}

// TestDenyFloorDropsNestedExfilSecretDecryptOverrides asserts that even a
// profile override that tries to allowlist a nested secret-value / object-content
// / decrypt command is dropped by the AWS deny floor: the value-returning verb is
// floored, while metadata-only reads under the same service stay allowable.
func TestDenyFloorDropsNestedExfilSecretDecryptOverrides(t *testing.T) {
	t.Parallel()
	p, err := newWithBinary("/usr/bin/aws")
	require.NoError(t, err)

	floored := [][]string{
		{"secretsmanager", "get-secret-value"},
		{"s3", "cp"},
		{"s3", "mv"},
		{"s3", "sync"},
		{"s3api", "get-object"},
		{"s3api", "get-object-attributes"},
		{"s3api", "get-object-torrent"},
		{"kms", "decrypt"},
		{"ssm", "get-parameter"},
		{"ssm", "get-parameters"},
		{"ssm", "get-parameters-by-path"},
	}
	// Metadata-only reads under the same services must remain allowable: the
	// floor targets secret VALUES, object CONTENTS, and decryption, not listing
	// or describing.
	metadataOnly := [][]string{
		{"secretsmanager", "describe-secret"},
		{"secretsmanager", "list-secrets"},
		{"s3api", "head-object"},
		{"s3api", "list-objects-v2"},
		{"ssm", "describe-parameters"},
		{"kms", "describe-key"},
	}

	override := allowlistJSON(t, append(append([][]string{}, floored...), metadataOnly...))
	loaded, err := cloud.LoadCommandAllowlist(override, p.DenyFloorAdditions())
	require.NoError(t, err)

	for _, argv := range floored {
		assert.Falsef(t, loaded.Allows(argv), "override must not re-enable floored %v", argv)
	}
	for _, argv := range metadataOnly {
		assert.Truef(t, loaded.Allows(argv), "metadata-only %v must stay allowable", argv)
	}
}

// allowlistJSON writes a command allowlist document with the given subcommand
// paths to a temp file and returns its path, the seam LoadCommandAllowlist reads
// a profile override through.
func allowlistJSON(t *testing.T, paths [][]string) string {
	t.Helper()
	var doc cloud.CommandAllowlist
	for _, p := range paths {
		doc.Commands = append(doc.Commands, cloud.Command{Path: strings.Join(p, " "), Description: "test"})
	}
	b, err := json.Marshal(doc)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "commands.json")
	require.NoError(t, os.WriteFile(path, b, 0o600))
	return path
}

func TestEnvPassthroughForwardsProfileAndRegionNames(t *testing.T) {
	p, err := newWithBinary("/usr/bin/aws")
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
