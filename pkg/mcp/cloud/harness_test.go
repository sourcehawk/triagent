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
