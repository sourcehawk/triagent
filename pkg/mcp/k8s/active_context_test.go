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
	require.NoError(t, WriteActiveContextFile(dir, "alpha"))
	body, err := os.ReadFile(filepath.Join(dir, "active-context"))
	require.NoError(t, err)
	assert.Equal(t, "alpha", string(body))
}

func TestWriteActiveContextFile_Overwrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, WriteActiveContextFile(dir, "alpha"))
	require.NoError(t, WriteActiveContextFile(dir, "beta"))
	body, err := os.ReadFile(filepath.Join(dir, "active-context"))
	require.NoError(t, err)
	assert.Equal(t, "beta", string(body))
}

func TestWriteActiveContextFile_LeavesNoTmpResidue(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, WriteActiveContextFile(dir, "x"))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".tmp", "no .tmp file should survive a successful write")
	}
}

func TestWriteActiveContextFile_ErrOnMissingDir(t *testing.T) {
	t.Parallel()
	err := WriteActiveContextFile("/nonexistent/path/that/does/not/exist", "x")
	require.Error(t, err)
}

func TestReadActiveContextFile_MissingReturnsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	name, err := readActiveContextFile(dir)
	require.NoError(t, err, "missing file is the launcher-didn't-pre-select case, not an error")
	assert.Equal(t, "", name)
}

func TestReadActiveContextFile_TrimsWhitespace(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "active-context"), []byte("  alpha\n"), 0o600))
	name, err := readActiveContextFile(dir)
	require.NoError(t, err)
	assert.Equal(t, "alpha", name)
}

func TestReadActiveContextFile_EmptyFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "active-context"), []byte("   \n"), 0o600))
	name, err := readActiveContextFile(dir)
	require.NoError(t, err)
	assert.Equal(t, "", name, "whitespace-only file is treated as no pre-selection")
}

// hydrateFromActiveContext is the startup-path equivalent of an agent-
// driven switch_context call. The four no-op paths must all leave the
// snapshot nil rather than panic — pre-selection is opportunistic and
// the agent can always call switch_context itself.

func TestHydrateFromActiveContext_NoSessionDir(t *testing.T) {
	t.Parallel()
	kit := &ToolKit{
		kubeconfigPath: writeKubeconfig(t, "", "alpha"),
		allowlist:      &Allowlist{Kinds: []Kind{}},
	}
	kit.hydrateFromActiveContext()
	assert.Nil(t, kit.snapshot.Load())
}

func TestHydrateFromActiveContext_FileMissing(t *testing.T) {
	t.Parallel()
	kit := &ToolKit{
		kubeconfigPath: writeKubeconfig(t, "", "alpha"),
		allowlist:      &Allowlist{Kinds: []Kind{}},
		sessionDir:     t.TempDir(), // no active-context inside
	}
	kit.hydrateFromActiveContext()
	assert.Nil(t, kit.snapshot.Load())
}

func TestHydrateFromActiveContext_FileEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "active-context"), []byte(""), 0o600))
	kit := &ToolKit{
		kubeconfigPath: writeKubeconfig(t, "", "alpha"),
		allowlist:      &Allowlist{Kinds: []Kind{}},
		sessionDir:     dir,
	}
	kit.hydrateFromActiveContext()
	assert.Nil(t, kit.snapshot.Load())
}

// When the named context isn't in the kubeconfig, buildSnapshot fails;
// hydrate must swallow the error and leave snapshot nil so the agent
// hits the standard "no active kubernetes context" error path.
func TestHydrateFromActiveContext_UnknownContextLeavesSnapshotNil(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "active-context"), []byte("does-not-exist"), 0o600))
	kit := &ToolKit{
		kubeconfigPath: writeKubeconfig(t, "", "alpha"),
		allowlist:      &Allowlist{Kinds: []Kind{}},
		sessionDir:     dir,
	}
	kit.hydrateFromActiveContext()
	assert.Nil(t, kit.snapshot.Load())
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
			_ = WriteActiveContextFile(dir, name)
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
