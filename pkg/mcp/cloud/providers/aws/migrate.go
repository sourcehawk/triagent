package aws

import (
	"fmt"
	"os"
	"strings"
)

// StripManagedBlocksFromConfig removes triagent's # BEGIN/# END
// triagent-cloud-* blocks from the operator's AWS config at path, a one-time
// cleanup of the old in-place approach now that triagent generates a separate
// owned config. It is a no-op (changed=false) when the file is absent or carries
// no managed blocks, and writes atomically only when it changed something, so it
// is safe to call on every launcher start. Operator-authored profiles outside
// the managed blocks are preserved.
func StripManagedBlocksFromConfig(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("aws: read %s: %w", path, err)
	}
	orig := string(b)
	if !strings.Contains(orig, "# BEGIN triagent-cloud-") {
		return false, nil
	}
	stripped := stripManagedBlocks(orig)
	if stripped == orig {
		return false, nil
	}
	if err := atomicWrite(path, []byte(stripped)); err != nil {
		return false, err
	}
	return true, nil
}
