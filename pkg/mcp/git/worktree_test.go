package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// worktreeTestServer constructs a Server with worktreeRootOverride
// set to a t.TempDir() subdir so this test's worktree bookkeeping stays
// isolated from parallel tests.
func worktreeTestServer(t *testing.T, owner, name string) (*Server, string) {
	t.Helper()
	cacheDir, repoDir := initFixtureRepo(t, owner, name)
	return &Server{
		owner: owner, name: name, cacheDir: cacheDir,
		worktreeRootOverride: filepath.Join(t.TempDir(), "wt-root"),
	}, repoDir
}

func TestNewWorktreeBranch_FormatsCorrectly(t *testing.T) {
	t.Parallel()
	name := newWorktreeBranch(42)
	require.Truef(t, strings.HasPrefix(name, "triagent-proposal/42-"), "got %q", name)
	suffix := strings.TrimPrefix(name, "triagent-proposal/42-")
	require.Len(t, suffix, 8)
}

func TestCreateWorktree_AddsWorktreeOffBaseRef(t *testing.T) {
	t.Parallel()
	s, repoDir := worktreeTestServer(t, "o", "n")
	branch := "triagent-proposal/1-aaaaaaaa"
	wt, err := s.createWorktree(context.Background(), repoDir, branch, "main")
	require.NoError(t, err)
	require.DirExists(t, wt)
	// .git inside a worktree is a FILE (not a dir), pointing back at
	// the parent's .git/worktrees/<name>/.
	info, err := os.Stat(filepath.Join(wt, ".git"))
	require.NoError(t, err)
	require.False(t, info.IsDir())

	// Cleanup
	require.NoError(t, s.removeWorktree(context.Background(), repoDir, wt))
	_, err = os.Stat(wt)
	require.True(t, os.IsNotExist(err))
}

func TestCreateWorktree_DefaultsBaseRefToMain(t *testing.T) {
	t.Parallel()
	s, repoDir := worktreeTestServer(t, "o", "n")
	branch := "triagent-proposal/2-bbbbbbbb"
	wt, err := s.createWorktree(context.Background(), repoDir, branch, "")
	require.NoError(t, err)
	defer func() { _ = s.removeWorktree(context.Background(), repoDir, wt) }()
	require.DirExists(t, wt)
}

func TestSweepStaleWorktrees_RemovesOlderThanCutoff(t *testing.T) {
	t.Parallel()
	s, repoDir := worktreeTestServer(t, "o", "n")
	// Create a worktree, age it past the cutoff via os.Chtimes, then sweep.
	branch := "triagent-proposal/9-bbbbbbbb"
	wt, err := s.createWorktree(context.Background(), repoDir, branch, "main")
	require.NoError(t, err)
	past := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(wt, past, past))
	require.NoError(t, s.sweepStaleWorktrees(context.Background(), repoDir, time.Hour))
	_, err = os.Stat(wt)
	require.True(t, os.IsNotExist(err), "stale worktree should have been removed")
}

func TestSweepStaleWorktrees_LeavesYoungAlone(t *testing.T) {
	t.Parallel()
	s, repoDir := worktreeTestServer(t, "o", "n")
	branch := "triagent-proposal/1-cccccccc"
	wt, err := s.createWorktree(context.Background(), repoDir, branch, "main")
	require.NoError(t, err)
	defer func() { _ = s.removeWorktree(context.Background(), repoDir, wt) }()
	require.NoError(t, s.sweepStaleWorktrees(context.Background(), repoDir, time.Hour))
	_, err = os.Stat(wt)
	require.NoError(t, err, "fresh worktree should still exist")
}

func TestSweepStaleWorktrees_LeavesOtherReposAlone(t *testing.T) {
	// Sweep is per-server-repo. A stale worktree from a different repo
	// (different owner/name prefix) must be left alone.
	t.Parallel()
	s, repoDir := worktreeTestServer(t, "o", "n")

	// Pre-create a stale dir under the test's worktreeRoot with a
	// different prefix. Using the same overridden root ensures the sweep
	// can actually see it (and prove it leaves it alone).
	other := filepath.Join(s.worktreeRoot(), "different-repo-foo")
	require.NoError(t, os.MkdirAll(other, 0o700))
	past := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(other, past, past))
	// No defer os.RemoveAll(other) needed — t.TempDir cleanup handles it.

	require.NoError(t, s.sweepStaleWorktrees(context.Background(), repoDir, time.Hour))

	// The other-repo dir must still exist.
	_, err := os.Stat(other)
	require.NoError(t, err, "different-repo dir must NOT be swept by this server")
}
