package gcp

import (
	"testing"

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
