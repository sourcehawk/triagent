package gcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sourcehawk/triagent/pkg/mcp/cloud"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewResolvesBinaryAndName(t *testing.T) {
	t.Parallel()
	p, err := newWithBinary("/usr/bin/gcloud")
	require.NoError(t, err)
	assert.Equal(t, "gcp", p.Name())
	assert.Equal(t, "/usr/bin/gcloud", p.Binary())
}

// TestNewResolvesBinaryToAbsolutePath proves New stores an absolute binary path
// even when PATH resolution would yield a relative one, so a later subprocess
// env/PATH change cannot redirect what executes. The provider's CLI is dropped
// into a temp dir reachable through a relative PATH entry; the resolved binary
// must come back absolute.
func TestNewResolvesBinaryToAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "gcloud")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755))

	cwd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	require.NoError(t, os.Chdir(dir))

	// "." is a relative PATH entry; exec.LookPath("gcloud") resolves to "gcloud"
	// (relative) under it.
	t.Setenv("PATH", ".")

	p, err := New()
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(p.Binary()),
		"New must store an absolute binary path, got %q", p.Binary())
}

func TestDefaultAllowlistLoadsEmbeddedJSON(t *testing.T) {
	t.Parallel()
	p, err := newWithBinary("/usr/bin/gcloud")
	require.NoError(t, err)
	allow := p.DefaultAllowlist()
	require.NotNil(t, allow)
	assert.NotEmpty(t, allow.Commands, "embedded default_commands.json should ship read-only commands")
}

func TestDefaultAllowlistIncludesProjectsList(t *testing.T) {
	t.Parallel()
	p, err := newWithBinary("/usr/bin/gcloud")
	require.NoError(t, err)
	assert.True(t, p.DefaultAllowlist().Allows([]string{"projects", "list", "--format=json"}),
		"Inventory needs `projects list` on the allowlist")
}

func TestDefaultAllowlistCoversInvestigativeAxes(t *testing.T) {
	t.Parallel()
	p, err := newWithBinary("/usr/bin/gcloud")
	require.NoError(t, err)
	allow := p.DefaultAllowlist()
	// One representative read-only command per investigative axis. Exact-match
	// allowlist, so each is the complete invariant verb chain.
	axes := [][]string{
		{"projects", "list"},                                       // inventory
		{"compute", "firewall-rules", "list"},                      // reachability
		{"projects", "get-iam-policy"},                             // permissions / IAM read
		{"container", "clusters", "describe"},                      // cluster / GKE describe
		{"logging", "read"},                                        // logs read
		{"logging", "logs", "list"},                                // audit read
	}
	for _, argv := range axes {
		assert.Truef(t, allow.Allows(argv), "expected %v on the allowlist", argv)
	}
}

func TestDenyFloorAdditionsCoverDangerousGCPSubcommands(t *testing.T) {
	t.Parallel()
	p, err := newWithBinary("/usr/bin/gcloud")
	require.NoError(t, err)
	floor := p.DenyFloorAdditions()
	for _, want := range []string{
		"compute ssh",
		"compute scp",
		"functions call",
		"compute reset-windows-password",
	} {
		assert.Containsf(t, floor.Subcommands, want, "expected %q on the gcp deny-floor additions", want)
	}
}

// TestDenyFloorDropsNestedExfilDecryptOverrides asserts that even a profile
// override that tries to allowlist a nested object-content / decrypt command is
// dropped by the GCP deny floor, while metadata-only reads under the same
// services stay allowable. (`gcloud secrets versions access` is already covered
// by the base `secrets` prefix and is not re-listed here.)
func TestDenyFloorDropsNestedExfilDecryptOverrides(t *testing.T) {
	t.Parallel()
	p, err := newWithBinary("/usr/bin/gcloud")
	require.NoError(t, err)

	floored := [][]string{
		{"storage", "cp"},
		{"storage", "mv"},
		{"storage", "rsync"},
		{"storage", "cat"},
		{"kms", "decrypt"},
	}
	// Metadata-only reads must remain allowable: the floor targets object
	// CONTENTS and decryption, not listing or describing.
	metadataOnly := [][]string{
		{"storage", "ls"},
		{"storage", "buckets", "describe"},
		{"kms", "keys", "list"},
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
