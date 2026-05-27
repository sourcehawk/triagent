package preflight

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureSessionsClone_EmptyRepoEmptyDirInitsLocal(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := filepath.Join(t.TempDir(), "sessions")
	err := EnsureSessionsClone(context.Background(), SessionsCloneOpts{
		Repo:    "",
		Dir:     dir,
		Offline: false,
	})
	require.NoError(t, err)
	info, err := os.Stat(filepath.Join(dir, ".git"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestEnsureSessionsClone_EmptyRepoEmptyDirOfflineInitsLocal(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := filepath.Join(t.TempDir(), "sessions")
	err := EnsureSessionsClone(context.Background(), SessionsCloneOpts{
		Repo:    "",
		Dir:     dir,
		Offline: true,
	})
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, ".git"))
	require.NoError(t, err)
}

func TestEnsureSessionsClone_OfflineWithRepoButEmptyDirFails(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "sessions")
	err := EnsureSessionsClone(context.Background(), SessionsCloneOpts{
		Repo:    "my-org/sessions",
		Dir:     dir,
		Offline: true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "offline")
}
