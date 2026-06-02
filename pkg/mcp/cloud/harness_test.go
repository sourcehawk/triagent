package cloud

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecCLIExitCode surfaces the child's exit code without treating a
// non-zero exit as a Go error: a CLI that exits 1 on "not found" is a normal
// result the agent should see, not a harness failure.
func TestExecCLIExitCode(t *testing.T) {
	t.Parallel()
	r, err := execCLI(context.Background(), "/bin/false", nil, nil, 4096)
	require.NoError(t, err, "non-zero exit should not be a Go error")
	assert.Equal(t, 1, r.ExitCode)
}

// TestExecCLICapturesStderr proves a non-zero exit carries the child's stderr,
// the context gcloud/aws write errors to. Without it run_cli would surface an
// empty stdout and no explanation for the failure.
func TestExecCLICapturesStderr(t *testing.T) {
	t.Parallel()
	// /bin/sh here is only the test fixture producing a stderr write + nonzero
	// exit; the harness itself never shells (see harness_security_test.go).
	r, err := execCLI(context.Background(), "/bin/sh",
		[]string{"-c", "echo boom 1>&2; exit 3"}, nil, 4096)
	require.NoError(t, err, "non-zero exit should not be a Go error")
	assert.Equal(t, 3, r.ExitCode)
	assert.Contains(t, r.Stderr, "boom", "stderr must be captured")
}

// TestExecCLITruncatesStderr caps stderr at the same limit as stdout so a
// noisy provider error cannot blow the context budget.
func TestExecCLITruncatesStderr(t *testing.T) {
	t.Parallel()
	r, err := execCLI(context.Background(), "/bin/sh",
		[]string{"-c", "printf '%0.sx' $(seq 1 100) 1>&2; exit 1"}, nil, 10)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(r.Stderr), 10, "stderr exceeded limit")
}

// TestExecCLICapsLargeOutputWithoutBuffering drives a payload orders of
// magnitude past the limit through a shell-free command (head reading 8MB from
// /dev/zero) and asserts the captured stdout is capped at the limit with
// Truncated set, so a command emitting a very large response cannot retain
// unbounded bytes in memory. The cap is effective during the run, not a
// post-hoc slice of a fully buffered output.
func TestExecCLICapsLargeOutputWithoutBuffering(t *testing.T) {
	t.Parallel()
	const limit = 1024
	r, err := execCLI(context.Background(), "/usr/bin/head",
		[]string{"-c", "8388608", "/dev/zero"}, nil, limit)
	require.NoError(t, err)
	assert.True(t, r.Truncated, "an output far larger than limit must be flagged truncated")
	assert.LessOrEqual(t, len(r.Stdout), limit, "captured stdout must be capped at limit, not the full 8MB payload")
}
