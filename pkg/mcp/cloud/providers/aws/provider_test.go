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

// TestNewResolvesBinaryToAbsolutePath proves New stores an absolute binary path
// even when PATH resolution would yield a relative one, so a later subprocess
// env/PATH change cannot redirect what executes. The CLI is dropped into a temp
// dir reachable through a relative PATH entry; the resolved binary must come
// back absolute.
func TestNewResolvesBinaryToAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "aws")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755))

	cwd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	require.NoError(t, os.Chdir(dir))

	// "." is a relative PATH entry; exec.LookPath("aws") resolves to "aws"
	// (relative) under it.
	t.Setenv("PATH", ".")

	p, err := New()
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(p.Binary()),
		"New must store an absolute binary path, got %q", p.Binary())
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

func TestConfiguredTargetsEmptyForSingleAccount(t *testing.T) {
	p, err := newWithBinary("/usr/bin/aws")
	require.NoError(t, err)
	assert.Nil(t, p.ConfiguredTargets())
}

func TestConfiguredTargetsFromAccounts(t *testing.T) {
	p := providerWithAccounts(t, "prod-aws", []Account{
		{ID: "111111111111", RoleARN: "arn:aws:iam::111111111111:role/r"},
		{ID: "222222222222", RoleARN: "arn:aws:iam::222222222222:role/r"},
	})
	targets := p.ConfiguredTargets()
	require.Len(t, targets, 2)
	assert.Equal(t, "111111111111", targets[0].ID)
	assert.Equal(t, "111111111111", targets[0].Name)
	assert.Equal(t, "222222222222", targets[1].ID)
}

func TestActiveTargetEnvUsesGeneratedProfileName(t *testing.T) {
	p := providerWithAccounts(t, "prod-aws", []Account{
		{ID: "111111111111", RoleARN: "arn:aws:iam::111111111111:role/r"},
	})
	assert.Equal(t, []string{"AWS_PROFILE=triagent-cloud-prod-aws-111111111111"}, p.ActiveTargetEnv("111111111111"))
}

// TestActiveTargetEnvSingleAccountPassthrough proves the legacy single-account
// provider (no alias, no accounts) treats the active id as the profile name
// directly, reproducing today's AWS_PROFILE=<id> behavior.
func TestActiveTargetEnvSingleAccountPassthrough(t *testing.T) {
	p, err := newWithBinary("/usr/bin/aws")
	require.NoError(t, err)
	assert.Equal(t, []string{"AWS_PROFILE=ro"}, p.ActiveTargetEnv("ro"))
}

// providerWithAccounts builds an aws provider with a generated-profile config
// pointed at a temp AWS config file so construction's writeManagedProfiles call
// does not touch the developer's ~/.aws/config.
func providerWithAccounts(t *testing.T, alias string, accs []Account) *Provider {
	t.Helper()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "config"))
	p, err := newWithBinary("/usr/bin/aws", Options{Alias: alias, SourceProfile: "sso-admin", Accounts: accs})
	require.NoError(t, err)
	return p
}
