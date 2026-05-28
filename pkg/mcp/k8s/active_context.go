package k8s

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ActiveContextFile is the filename, inside the session dir, the k8s
// MCP writes on a successful switch_context. Exported so triagent's
// launcher (internal/server/prom_endpoint.go) reads from the same
// symbol rather than duplicating the string literal.
const ActiveContextFile = "active-context"

// readActiveContextFile returns the trimmed contents of
// <sessionDir>/active-context. A missing file is not an error: it's
// the launcher-didn't-pre-select case. An empty / whitespace-only
// file returns the empty string with no error so callers handle
// "no pre-selection" uniformly with "no file".
func readActiveContextFile(sessionDir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(sessionDir, ActiveContextFile))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

// hydrateFromActiveContext is called once at MCP startup. When the
// launcher has pre-seeded <sessionDir>/active-context (because the
// operator pre-selected a cluster on the session-start form), build
// the snapshot now so the first agent tool call sees an active
// context — no round-trip through switch_context required.
//
// Best-effort: a missing file, an unreadable file, an empty name, or
// a failed buildSnapshot all leave the toolkit context-less and let
// the agent fall back to the standard switch_context flow. Failures
// are logged to stderr (the MCP's only out-of-band channel).
func (t *ToolKit) hydrateFromActiveContext() {
	if t.sessionDir == "" {
		return
	}
	name, err := readActiveContextFile(t.sessionDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "k8s: read active-context: %v\n", err)
		return
	}
	if name == "" {
		return
	}
	snap, err := t.buildSnapshot(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "k8s: hydrate active-context %q: %v\n", name, err)
		return
	}
	t.snapshot.Store(snap)
}

// WriteActiveContextFile writes contextName to <sessionDir>/active-context
// atomically: write to a per-call unique tmp file, then rename onto the
// final path. Best-effort — callers treat errors as warnings, not
// failures. The tmp filename includes a random suffix so concurrent
// callers don't trample each other's in-flight writes; the final
// rename is what determines the visible content.
//
// Exported so the launcher (internal/server) can pre-seed the file at
// preflight when the operator selects a cluster on the session-start
// form — the k8s MCP's hydrate-on-startup path then picks it up.
func WriteActiveContextFile(sessionDir, contextName string) error {
	dst := filepath.Join(sessionDir, ActiveContextFile)
	tmp, err := os.CreateTemp(sessionDir, ActiveContextFile+".tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write([]byte(contextName)); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
