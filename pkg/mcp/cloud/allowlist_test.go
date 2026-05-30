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

func TestAllowsMatchesLongestPathPrefix(t *testing.T) {
	t.Parallel()
	al := &CommandAllowlist{Commands: []Command{{Path: "compute firewall-rules list"}}}
	assert.True(t, al.Allows([]string{"compute", "firewall-rules", "list", "--project", "prod"}),
		"argv whose leading tokens match an allowed path should pass")
	assert.False(t, al.Allows([]string{"compute", "firewall-rules", "delete"}),
		"a different verb under the same group must not be allowed")
}
