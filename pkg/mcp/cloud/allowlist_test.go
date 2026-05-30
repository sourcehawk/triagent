package cloud

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "allowlist.json")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

func TestLoadCommandAllowlistDropsDenyFloor(t *testing.T) {
	t.Parallel()
	// JSON that tries to allow a deny-floored subcommand.
	path := writeTemp(t, `{"commands":[{"path":"projects list"},{"path":"secrets versions access"}]}`)
	al, err := LoadCommandAllowlist(path, DenyFloor{})
	require.NoError(t, err)
	assert.False(t, al.Allows([]string{"secrets", "versions", "access"}),
		"deny floor must drop secrets access regardless of config")
	assert.True(t, al.Allows([]string{"projects", "list"}), "projects list should be allowed")
}

func TestLoadCommandAllowlistUsesEmbeddedDefaultWhenPathEmpty(t *testing.T) {
	t.Parallel()
	// The parent package ships no provider commands of its own; an empty path
	// yields the empty embedded default, not an error.
	al, err := LoadCommandAllowlist("", DenyFloor{})
	require.NoError(t, err)
	assert.NotNil(t, al, "expected a non-nil allowlist for the empty default")
}

func TestLoadCommandAllowlistMergesProviderDenyFloorAdditions(t *testing.T) {
	t.Parallel()
	path := writeTemp(t, `{"commands":[{"path":"compute instances list"},{"path":"compute ssh foo"}]}`)
	extra := DenyFloor{Subcommands: []string{"compute ssh"}}
	al, err := LoadCommandAllowlist(path, extra)
	require.NoError(t, err)
	assert.False(t, al.Allows([]string{"compute", "ssh", "foo"}),
		"provider deny-floor addition must drop compute ssh")
	assert.True(t, al.Allows([]string{"compute", "instances", "list"}),
		"compute instances list should remain allowed")
}

func TestAllowsMatchesVerbChainAsPrefix(t *testing.T) {
	t.Parallel()
	al := &CommandAllowlist{Commands: []Command{{Path: "compute firewall-rules list"}}}
	assert.True(t, al.Allows([]string{"compute", "firewall-rules", "list", "--project", "prod"}),
		"argv whose leading tokens match an allowed path should pass")
	assert.False(t, al.Allows([]string{"compute", "firewall-rules", "delete"}),
		"a different verb under the same group must not be allowed")
}

func TestAllowsAcceptsResourceOperandAfterVerbChain(t *testing.T) {
	t.Parallel()
	al := &CommandAllowlist{Commands: []Command{{Path: "compute instances describe"}}}
	// A describe/get command takes a resource operand as a trailing positional;
	// the allowlisted verb chain must match as a prefix so the operand rides
	// through. There is no shell, so the operand is an inert argument.
	assert.True(t, al.Allows([]string{"compute", "instances", "describe", "my-vm", "--project", "prod"}),
		"an allowlisted verb chain followed by a resource operand must pass")
	assert.True(t, al.Allows([]string{"compute", "instances", "describe", "my-vm", "us-vm-2"}),
		"trailing positionals after the verb chain must pass")
}

func TestAllowsRejectsArgvShorterThanPath(t *testing.T) {
	t.Parallel()
	al := &CommandAllowlist{Commands: []Command{{Path: "compute instances describe"}}}
	assert.False(t, al.Allows([]string{"compute", "instances"}),
		"an argv shorter than the allowlisted path is not a match")
}

func TestAllowsRejectsDifferentVerbAtPrefixDepth(t *testing.T) {
	t.Parallel()
	al := &CommandAllowlist{Commands: []Command{{Path: "compute instances describe"}}}
	assert.False(t, al.Allows([]string{"compute", "instances", "delete", "my-vm"}),
		"a different verb at the same depth must not match")
}
