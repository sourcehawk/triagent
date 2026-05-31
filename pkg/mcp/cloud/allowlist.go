package cloud

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// defaultCommandsJSON is the parent package's embedded default allowlist. It is
// intentionally empty: provider command sets ship in each provider's own
// default_commands.json (pkg/mcp/cloud/providers/<name>). This anchor lets the
// shared loader compile and gives LoadCommandAllowlist("", …) a valid document.
//
//go:embed default_commands.json
var defaultCommandsJSON []byte

// Command is one entry in the command allowlist. Path is the normalized
// subcommand path the allowlist matches against (for example "projects list" or
// "compute firewall-rules list"). Description carries the investigative axis the
// command serves (prose only).
type Command struct {
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
}

// CommandAllowlist is the decoded allowlist document: the positive set of
// subcommand paths run_cli permits.
type CommandAllowlist struct {
	Commands []Command `json:"commands"`
}

// DenyFloor is the always-on set of subcommands, flags, and argument-value
// prefixes that the config can never re-enable. The base floor lives in this
// package; a Provider contributes provider-specific additions through
// DenyFloorAdditions, mirroring how k8s.LoadAllowlist always drops Secret.
type DenyFloor struct {
	Subcommands []string `json:"subcommands,omitempty"`
	Flags       []string `json:"flags,omitempty"`
	ArgPrefixes []string `json:"arg_prefixes,omitempty"`
}

// denyFloor is the base floor. Config can never re-enable these; they are
// filtered out of any loaded allowlist and rejected in argv validation. The
// floor covers credential-reading and identity/endpoint-redirecting subcommands
// and flags, plus argument prefixes that read local files or reach the network
// (local-file read and SSRF vectors).
var denyFloor = DenyFloor{
	Subcommands: []string{"secrets", "ssh", "scp", "cp", "sync", "auth", "config"},
	Flags: []string{
		"--impersonate-service-account", "--account", "--profile",
		"--endpoint-url", "--cli-input-json", "--cli-input-yaml", "--configuration",
		"--flags-file", "--access-token-file",
	},
	ArgPrefixes: []string{"file://", "fileb://", "@", "http://", "https://"},
}

// mergeDenyFloor combines the base floor with provider additions into one floor.
func mergeDenyFloor(extra DenyFloor) DenyFloor {
	return DenyFloor{
		Subcommands: append(append([]string{}, denyFloor.Subcommands...), extra.Subcommands...),
		Flags:       append(append([]string{}, denyFloor.Flags...), extra.Flags...),
		ArgPrefixes: append(append([]string{}, denyFloor.ArgPrefixes...), extra.ArgPrefixes...),
	}
}

// LoadCommandAllowlist returns the command allowlist from path, or the embedded
// default when path is empty, then filters out every command whose subcommand
// path falls under the base deny floor plus the provider's extra additions. A
// too-broad override can never re-enable a floored command — the filter is
// applied identically regardless of input, the LoadAllowlist pattern.
func LoadCommandAllowlist(path string, extra DenyFloor) (*CommandAllowlist, error) {
	data := defaultCommandsJSON
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read command allowlist %q: %w", path, err)
		}
		data = b
	}

	var list CommandAllowlist
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse command allowlist: %w", err)
	}

	for _, c := range list.Commands {
		if c.Path == "" {
			return nil, fmt.Errorf("command allowlist entry missing path: %+v", c)
		}
	}
	return filterAllowlist(&list, extra), nil
}

// filterAllowlist returns a copy of list with every command whose subcommand
// path falls under the base deny floor plus extra dropped. Applied identically
// to a loaded file and to a provider's in-memory default, so neither source can
// advertise a floored command.
func filterAllowlist(list *CommandAllowlist, extra DenyFloor) *CommandAllowlist {
	floor := mergeDenyFloor(extra)
	out := &CommandAllowlist{Commands: make([]Command, 0, len(list.Commands))}
	for _, c := range list.Commands {
		if c.Path == "" || floor.blocks(normalizePath(c.Path)) {
			continue
		}
		out.Commands = append(out.Commands, c)
	}
	return out
}

// Allows reports whether an allowlisted command path is a token-wise prefix of
// argv's leading positional subcommand path. Flag tokens and their values do
// not participate; only the leading positionals do. Prefix rather than exact
// match lets a describe/get verb chain carry its trailing resource operand
// (`compute instances describe my-vm`): there is no shell, so a trailing token
// is an inert argument to the already-dispatched subcommand. Surplus tokens
// that are shell-control sequences are caught separately in validateArgv.
func (a *CommandAllowlist) Allows(argv []string) bool {
	path := subcommandPath(argv)
	for _, c := range a.Commands {
		if pathHasPrefix(path, normalizePath(c.Path)) {
			return true
		}
	}
	return false
}

// blocks reports whether an allowlist entry's normalized subcommand path is
// prefix-comparable to any floored subcommand, in either direction:
//
//   - a floor entry is a token-prefix of path: path sits UNDER a denied path, so
//     allowing it runs a floored command directly ("secrets" floors "secrets
//     versions access", "compute ssh" floors "compute ssh foo");
//   - path is a token-prefix of a floor entry: path is a parent OF a denied path,
//     so allowing it re-admits the floored command through Allows' prefix match
//     (a bare "s3" entry would re-admit the floored "s3 cp").
//
// Both directions drop the entry. An entry that merely shares a leading token
// but diverges deeper ("compute instances list" vs floored "compute ssh") is
// prefix-comparable to neither and stays allowed.
func (d DenyFloor) blocks(path []string) bool {
	for _, s := range d.Subcommands {
		floor := normalizePath(s)
		if pathHasPrefix(path, floor) || pathHasPrefix(floor, path) {
			return true
		}
	}
	return false
}

// subcommandPath returns the leading positional tokens of argv, stopping at the
// first flag (a token beginning with "-"). These tokens form the subcommand
// path the allowlist and deny floor match against.
func subcommandPath(argv []string) []string {
	out := make([]string, 0, len(argv))
	for _, tok := range argv {
		if strings.HasPrefix(tok, "-") {
			break
		}
		out = append(out, tok)
	}
	return out
}

// normalizePath splits a space-separated command path ("compute firewall-rules
// list") into its tokens.
func normalizePath(path string) []string {
	return strings.Fields(path)
}

// pathHasPrefix reports whether prefix is a token-wise prefix of path.
func pathHasPrefix(path, prefix []string) bool {
	if len(prefix) == 0 || len(prefix) > len(path) {
		return false
	}
	for i := range prefix {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}
