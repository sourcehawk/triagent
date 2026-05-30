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
