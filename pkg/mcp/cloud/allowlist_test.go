package cloud

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "allowlist.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadCommandAllowlistDropsDenyFloor(t *testing.T) {
	t.Parallel()
	// JSON that tries to allow a deny-floored subcommand.
	path := writeTemp(t, `{"commands":[{"path":"projects list"},{"path":"secrets versions access"}]}`)
	al, err := LoadCommandAllowlist(path, DenyFloor{})
	if err != nil {
		t.Fatal(err)
	}
	if al.Allows([]string{"secrets", "versions", "access"}) {
		t.Fatal("deny floor must drop secrets access regardless of config")
	}
	if !al.Allows([]string{"projects", "list"}) {
		t.Fatal("projects list should be allowed")
	}
}

func TestLoadCommandAllowlistUsesEmbeddedDefaultWhenPathEmpty(t *testing.T) {
	t.Parallel()
	// The parent package ships no provider commands of its own; an empty path
	// yields the empty embedded default, not an error.
	al, err := LoadCommandAllowlist("", DenyFloor{})
	if err != nil {
		t.Fatal(err)
	}
	if al == nil {
		t.Fatal("expected a non-nil allowlist for the empty default")
	}
}

func TestLoadCommandAllowlistMergesProviderDenyFloorAdditions(t *testing.T) {
	t.Parallel()
	path := writeTemp(t, `{"commands":[{"path":"compute instances list"},{"path":"compute ssh foo"}]}`)
	extra := DenyFloor{Subcommands: []string{"compute ssh"}}
	al, err := LoadCommandAllowlist(path, extra)
	if err != nil {
		t.Fatal(err)
	}
	if al.Allows([]string{"compute", "ssh", "foo"}) {
		t.Fatal("provider deny-floor addition must drop compute ssh")
	}
	if !al.Allows([]string{"compute", "instances", "list"}) {
		t.Fatal("compute instances list should remain allowed")
	}
}

func TestAllowsMatchesLongestPathPrefix(t *testing.T) {
	t.Parallel()
	al := &CommandAllowlist{Commands: []Command{{Path: "compute firewall-rules list"}}}
	if !al.Allows([]string{"compute", "firewall-rules", "list", "--project", "prod"}) {
		t.Fatal("argv whose leading tokens match an allowed path should pass")
	}
	if al.Allows([]string{"compute", "firewall-rules", "delete"}) {
		t.Fatal("a different verb under the same group must not be allowed")
	}
}
