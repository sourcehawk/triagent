// Package system holds the launcher-bundled meta-playbooks: the
// master "investigation" entrypoint plus the workflow metas
// (playbook_proposal, followup_conversation, wiki_proposal). They
// ship with the launcher binary because the system's contract
// (where investigations start, how they handle follow-ups, how
// proposals get reviewed) needs to ride along with whatever triagent-mcp
// + launcher version is in use — drift between the binary and these
// playbooks would silently break the chat flow.
//
// The triagent-strategies MCP server treats this set as locked: user-dir
// files targeting a system id are silently dropped at load time. The
// editor disables save/delete/edit on locked entries.
//
// On startup the launcher calls Extract(rootDir) to materialise the
// embedded YAMLs onto disk under <rootDir>/system/<file> (the
// type-as-subdirectory layout the strategies loader expects), then
// passes <rootDir> to triagent-mcp via --system-playbooks-dir.
package system

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed *.yaml type.txt
var Files embed.FS

// Extract writes the embedded system playbooks (and the type.txt
// describing the slot) into <root>/system/<file>. Re-extracts every
// startup, overwriting on collisions — the embedded set is the
// source of truth, and an operator who has an old extract from a
// previous launcher build should always see the current set.
//
// Returns the path to <root>/system (the type slot) for callers that
// want to verify on-disk shape after extraction; the launcher passes
// <root> (the parent) to triagent-mcp.
func Extract(root string) (string, error) {
	typeDir := filepath.Join(root, "system")
	if err := os.MkdirAll(typeDir, 0o700); err != nil {
		return "", fmt.Errorf("create system playbooks dir %s: %w", typeDir, err)
	}
	entries, err := fs.ReadDir(Files, ".")
	if err != nil {
		return "", fmt.Errorf("read embedded system playbooks: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := Files.ReadFile(e.Name())
		if err != nil {
			return "", fmt.Errorf("read embedded %s: %w", e.Name(), err)
		}
		dest := filepath.Join(typeDir, e.Name())
		if err := os.WriteFile(dest, data, 0o600); err != nil {
			return "", fmt.Errorf("write %s: %w", dest, err)
		}
	}
	return typeDir, nil
}
