package k8s

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteActiveContextFile_Writes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, writeActiveContextFile(dir, "alpha"))
	body, err := os.ReadFile(filepath.Join(dir, "active-context"))
	require.NoError(t, err)
	assert.Equal(t, "alpha", string(body))
}

func TestWriteActiveContextFile_Overwrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, writeActiveContextFile(dir, "alpha"))
	require.NoError(t, writeActiveContextFile(dir, "beta"))
	body, err := os.ReadFile(filepath.Join(dir, "active-context"))
	require.NoError(t, err)
	assert.Equal(t, "beta", string(body))
}

func TestWriteActiveContextFile_LeavesNoTmpResidue(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, writeActiveContextFile(dir, "x"))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".tmp", "no .tmp file should survive a successful write")
	}
}

func TestWriteActiveContextFile_ErrOnMissingDir(t *testing.T) {
	t.Parallel()
	err := writeActiveContextFile("/nonexistent/path/that/does/not/exist", "x")
	require.Error(t, err)
}

func TestWriteActiveContextFile_ConcurrentWritesProduceValidFinalState(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const N = 16
	candidates := make([]string, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		name := fmt.Sprintf("ctx-%02d", i)
		candidates[i] = name
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_ = writeActiveContextFile(dir, name)
		}(name)
	}
	wg.Wait()
	body, err := os.ReadFile(filepath.Join(dir, "active-context"))
	require.NoError(t, err)
	assert.Contains(t, candidates, string(body),
		"final active-context must be one of the values that was written, not a torn write")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".tmp",
			"no tmp file should survive after all concurrent writes complete")
	}
}
