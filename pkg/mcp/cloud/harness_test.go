package cloud

import (
	"context"
	"testing"
)

// TestExecCLIExitCode surfaces the child's exit code without treating a
// non-zero exit as a Go error: a CLI that exits 1 on "not found" is a normal
// result the agent should see, not a harness failure.
func TestExecCLIExitCode(t *testing.T) {
	t.Parallel()
	r, err := execCLI(context.Background(), "/bin/false", nil, nil, 4096)
	if err != nil {
		t.Fatalf("non-zero exit should not be a Go error: %v", err)
	}
	if r.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", r.ExitCode)
	}
}
