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

// EnsurePlaybooksClone with no upstream repo configured must succeed and
// leave a usable local-only git checkout on disk so wiki/playbook/session
// commit calls downstream don't fail when the operator hasn't pointed at
// an upstream repo yet.
func TestEnsurePlaybooksClone_EmptyRepoEmptyDirInitsLocal(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := filepath.Join(t.TempDir(), "upstream-playbooks")
	err := EnsurePlaybooksClone(context.Background(), PlaybooksCloneOpts{
		Repo:    "",
		Dir:     dir,
		Offline: false,
	})
	require.NoError(t, err, "empty repo + empty dir + online should not fail")
	info, err := os.Stat(filepath.Join(dir, ".git"))
	require.NoError(t, err, "git init should have created a .git entry")
	assert.True(t, info.IsDir(), ".git should be a directory after init")
}

// Same as above but with offline=true — offline mode with no repo and no
// pre-seeded content should also gracefully degrade to a local-only
// checkout rather than fail. The old behavior asked the operator to
// either pre-seed or unset offline, neither of which made sense when no
// upstream repo was configured anyway.
func TestEnsurePlaybooksClone_EmptyRepoEmptyDirOfflineInitsLocal(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := filepath.Join(t.TempDir(), "upstream-playbooks")
	err := EnsurePlaybooksClone(context.Background(), PlaybooksCloneOpts{
		Repo:    "",
		Dir:     dir,
		Offline: true,
	})
	require.NoError(t, err, "empty repo + empty dir + offline should not fail")
	_, err = os.Stat(filepath.Join(dir, ".git"))
	require.NoError(t, err)
}

// Existing checkout is a no-op regardless of repo emptiness — we trust
// what's on disk.
func TestEnsurePlaybooksClone_ExistingGitCheckoutNoop(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := filepath.Join(t.TempDir(), "preexisting")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o700))
	// Drop a marker file so we can prove we didn't reinitialise.
	marker := filepath.Join(dir, ".git", "marker")
	require.NoError(t, os.WriteFile(marker, []byte("preserved"), 0o600))

	err := EnsurePlaybooksClone(context.Background(), PlaybooksCloneOpts{
		Repo:    "",
		Dir:     dir,
		Offline: false,
	})
	require.NoError(t, err)
	b, err := os.ReadFile(marker)
	require.NoError(t, err)
	assert.Equal(t, "preserved", string(b), "existing .git must not be wiped")
}

// Offline + repo configured + empty dir still fails — operator asked for
// offline so we won't clone, and there's nothing on disk to use. This is
// the existing behavior; the test pins it so we don't regress.
func TestEnsurePlaybooksClone_OfflineWithRepoButEmptyDirFails(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "upstream-playbooks")
	err := EnsurePlaybooksClone(context.Background(), PlaybooksCloneOpts{
		Repo:    "my-org/playbooks",
		Dir:     dir,
		Offline: true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "offline")
}
