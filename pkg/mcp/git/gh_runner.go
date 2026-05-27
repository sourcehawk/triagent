package git

import (
	"context"
	"os/exec"
)

// ghRunner is the seam tests inject. Production wraps exec.CommandContext;
// tests inject a stub that returns canned stdout/exit codes.
//
// Stdin is intentionally absent — none of our gh subcommands take it.
type ghRunner interface {
	Run(ctx context.Context, args ...string) (stdout []byte, stderr []byte, err error)
}

type realGhRunner struct{}

func (realGhRunner) Run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.Output()
	var errBuf []byte
	if ee, ok := err.(*exec.ExitError); ok {
		errBuf = ee.Stderr
	}
	return out, errBuf, err
}
