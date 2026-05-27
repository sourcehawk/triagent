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
// atomically: write to a per-call unique tmp file, then rename onto the
// final path. Best-effort — callers treat errors as warnings, not
// failures. The tmp filename includes a random suffix so concurrent
// callers don't trample each other's in-flight writes; the final
// rename is what determines the visible content.
func writeActiveContextFile(sessionDir, contextName string) error {
	dst := filepath.Join(sessionDir, activeContextFile)
	tmp, err := os.CreateTemp(sessionDir, activeContextFile+".tmp.*")
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
