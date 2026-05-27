package k8s

import (
	"os"
	"path/filepath"
)

// activeContextFile is the filename, inside the session dir, the k8s MCP
// writes on a successful switch_context. Read by triagent's launcher to
// know which kubeconfig context the agent is currently bound to. Other
// consumers can ignore it.
const activeContextFile = "active-context"

// writeActiveContextFile writes contextName to <sessionDir>/active-context
// atomically: write to a sibling tmp file, then rename. Best-effort —
// callers treat errors as warnings, not failures.
func writeActiveContextFile(sessionDir, contextName string) error {
	dst := filepath.Join(sessionDir, activeContextFile)
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, []byte(contextName), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
