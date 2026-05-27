package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// skipIfNoGit skips the test when the git binary isn't available in PATH.
func skipIfNoGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not found in PATH")
	}
}

// initGitRepo is a test helper: runs gitInitUserPlaybooksDir in dir and
// fails the test on any error.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, gitInitUserPlaybooksDir(context.Background(), dir), "gitInitUserPlaybooksDir")
}

// gitLogSubjects returns the raw subject lines for commits in dir.
// When relPath is non-empty it restricts the log to commits touching that path.
func gitLogSubjects(t *testing.T, dir, relPath string) []string {
	t.Helper()
	args := []string{"-C", dir, "log", "--format=%s"}
	if relPath != "" {
		args = append(args, "--", relPath)
	}
	out, err := exec.Command("git", args...).Output()
	require.NoError(t, err, "git log")
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// gitHead returns the short sha of HEAD in dir.
func gitHead(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	require.NoError(t, err, "git rev-parse HEAD")
	return strings.TrimSpace(string(out))
}

// writeAndCommit writes content to relPath (relative to dir) and calls
// commitPlaybookChange with the given message. Helper for multi-commit
// test setups.
func writeAndCommit(t *testing.T, dir, relPath, content, message string) {
	t.Helper()
	fullPath := filepath.Join(dir, relPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
	require.NoError(t, os.WriteFile(fullPath, []byte(content), 0o644))
	require.NoError(t, commitPlaybookChange(context.Background(), dir, relPath, message))
}

// TestGitInitUserPlaybooksDir_CreatesRepo ensures that a fresh dir gets a
// .git directory and an initial commit after the first call.
func TestGitInitUserPlaybooksDir_CreatesRepo(t *testing.T) {
	t.Parallel()
	skipIfNoGit(t)
	dir := t.TempDir()

	require.NoError(t, gitInitUserPlaybooksDir(context.Background(), dir))

	_, err := os.Stat(filepath.Join(dir, ".git"))
	require.NoError(t, err, ".git should exist after init")

	subjects := gitLogSubjects(t, dir, "")
	require.Len(t, subjects, 1, "expected exactly one initial commit")
	assert.Equal(t, "initial: user playbooks dir", subjects[0])
}

// TestGitInitUserPlaybooksDir_Idempotent ensures a second call is a no-op
// (the .git dir exists so the function returns early without creating another
// initial commit).
func TestGitInitUserPlaybooksDir_Idempotent(t *testing.T) {
	t.Parallel()
	skipIfNoGit(t)
	dir := t.TempDir()

	require.NoError(t, gitInitUserPlaybooksDir(context.Background(), dir))
	sha1 := gitHead(t, dir)

	// Second call must not modify the repo.
	require.NoError(t, gitInitUserPlaybooksDir(context.Background(), dir))
	sha2 := gitHead(t, dir)

	assert.Equal(t, sha1, sha2, "second init must not create a new commit")
}

// TestGitInitUserPlaybooksDir_StagesPreExistingFiles verifies that a file
// already on disk when init is called lands in the initial commit.
func TestGitInitUserPlaybooksDir_StagesPreExistingFiles(t *testing.T) {
	t.Parallel()
	skipIfNoGit(t)
	dir := t.TempDir()

	typeDir := filepath.Join(dir, "investigation")
	require.NoError(t, os.MkdirAll(typeDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(typeDir, "broker-crashloop.yaml"),
		[]byte("id: broker-crashloop\n"), 0o644))

	require.NoError(t, gitInitUserPlaybooksDir(context.Background(), dir))

	// The pre-existing file must appear in the initial commit's tree.
	out, err := exec.Command("git", "-C", dir, "show", "--name-only", "--format=", "HEAD").Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "broker-crashloop.yaml",
		"pre-existing file must be staged in the initial commit")
}

// TestCommitPlaybookChange_CommitsRealChange writes v1, commits, writes v2,
// and asserts a new commit landed with the right subject.
func TestCommitPlaybookChange_CommitsRealChange(t *testing.T) {
	t.Parallel()
	skipIfNoGit(t)
	dir := t.TempDir()
	initGitRepo(t, dir)

	relPath := filepath.Join("investigation", "test-playbook.yaml")

	writeAndCommit(t, dir, relPath, "id: test-playbook\nversion: 1\n", "test-playbook: initial")

	// Now write v2 and call commitPlaybookChange.
	require.NoError(t, os.WriteFile(filepath.Join(dir, relPath), []byte("id: test-playbook\nversion: 2\n"), 0o644))
	require.NoError(t, commitPlaybookChange(context.Background(), dir, relPath, "test-playbook: update"))

	subjects := gitLogSubjects(t, dir, relPath)
	require.GreaterOrEqual(t, len(subjects), 2, "expected at least 2 commits for this path")
	assert.Equal(t, "test-playbook: update", subjects[0], "newest commit message should be the update")
}

// TestCommitPlaybookChange_NoOpWhenIdentical writes a file, commits, writes
// the same content again, and verifies no new commit lands.
func TestCommitPlaybookChange_NoOpWhenIdentical(t *testing.T) {
	t.Parallel()
	skipIfNoGit(t)
	dir := t.TempDir()
	initGitRepo(t, dir)

	relPath := filepath.Join("investigation", "idempotent.yaml")
	content := "id: idempotent\nactive: true\n"

	writeAndCommit(t, dir, relPath, content, "idempotent: initial")
	sha1 := gitHead(t, dir)

	// Write the same bytes again.
	require.NoError(t, os.WriteFile(filepath.Join(dir, relPath), []byte(content), 0o644))
	require.NoError(t, commitPlaybookChange(context.Background(), dir, relPath, "should be skipped"))

	sha2 := gitHead(t, dir)
	assert.Equal(t, sha1, sha2, "no new commit should land for an identical write")
}

// TestCommitPlaybookChange_NoRepoIsNoOp calls commitPlaybookChange against a
// plain directory (no .git) and expects nil, no error.
func TestCommitPlaybookChange_NoRepoIsNoOp(t *testing.T) {
	t.Parallel()
	skipIfNoGit(t)
	dir := t.TempDir()

	err := commitPlaybookChange(context.Background(), dir, "some.yaml", "msg")
	assert.NoError(t, err, "should not error when no .git is present")
}

// TestGitLogForPlaybook_ReturnsCommits initialises a repo, writes three
// commits touching the same path, and asserts gitLogForPlaybook returns
// them newest-first with correct messages.
func TestGitLogForPlaybook_ReturnsCommits(t *testing.T) {
	t.Parallel()
	skipIfNoGit(t)
	dir := t.TempDir()
	initGitRepo(t, dir)

	relPath := filepath.Join("investigation", "multi.yaml")
	writeAndCommit(t, dir, relPath, "id: multi\nv: 1\n", "multi: v1")
	writeAndCommit(t, dir, relPath, "id: multi\nv: 2\n", "multi: v2")
	writeAndCommit(t, dir, relPath, "id: multi\nv: 3\n", "multi: v3")

	commits, err := gitLogForPlaybook(context.Background(), dir, relPath, "local", "", 10)
	require.NoError(t, err)
	require.Len(t, commits, 3)

	assert.Equal(t, "multi: v3", commits[0].Summary, "newest first")
	assert.Equal(t, "multi: v2", commits[1].Summary)
	assert.Equal(t, "multi: v1", commits[2].Summary)

	for _, c := range commits {
		assert.Len(t, c.Sha, 40, "sha should be a full 40-char hex string")
		assert.Equal(t, "local", c.Source)
		assert.NotEmpty(t, c.Date)
	}
}

// TestGitLogForPlaybook_BeforeCursor verifies that the before parameter
// returns only commits older than the given sha.
func TestGitLogForPlaybook_BeforeCursor(t *testing.T) {
	t.Parallel()
	skipIfNoGit(t)
	dir := t.TempDir()
	initGitRepo(t, dir)

	relPath := filepath.Join("investigation", "paged.yaml")
	writeAndCommit(t, dir, relPath, "id: paged\nv: 1\n", "paged: v1")
	writeAndCommit(t, dir, relPath, "id: paged\nv: 2\n", "paged: v2")
	writeAndCommit(t, dir, relPath, "id: paged\nv: 3\n", "paged: v3")

	// Get all three to find the sha of the newest.
	all, err := gitLogForPlaybook(context.Background(), dir, relPath, "local", "", 10)
	require.NoError(t, err)
	require.Len(t, all, 3)

	newestSha := all[0].Sha
	older, err := gitLogForPlaybook(context.Background(), dir, relPath, "local", newestSha, 10)
	require.NoError(t, err)
	require.Len(t, older, 2, "before cursor should exclude the newest commit")
	assert.Equal(t, "paged: v2", older[0].Summary)
	assert.Equal(t, "paged: v1", older[1].Summary)
}

// TestGitLogForPlaybook_LimitRespected ensures the limit parameter caps the
// result set.
func TestGitLogForPlaybook_LimitRespected(t *testing.T) {
	t.Parallel()
	skipIfNoGit(t)
	dir := t.TempDir()
	initGitRepo(t, dir)

	relPath := filepath.Join("investigation", "limited.yaml")
	for i := 1; i <= 5; i++ {
		writeAndCommit(t, dir, relPath,
			strings.Repeat("x", i), // distinct content to force commits
			"limited: v"+string(rune('0'+i)))
	}

	commits, err := gitLogForPlaybook(context.Background(), dir, relPath, "local", "", 3)
	require.NoError(t, err)
	assert.Len(t, commits, 3, "result must be capped at limit=3")
}

// TestGitLogForPlaybook_NoRepo returns nil/nil when the dir has no .git.
func TestGitLogForPlaybook_NoRepo(t *testing.T) {
	t.Parallel()
	skipIfNoGit(t)
	dir := t.TempDir()

	commits, err := gitLogForPlaybook(context.Background(), dir, "some.yaml", "local", "", 10)
	assert.NoError(t, err)
	assert.Nil(t, commits)
}

// TestGitShowAtCommit_ReturnsBody writes two commits for the same file and
// verifies that gitShowAtCommit returns the older body when asked.
func TestGitShowAtCommit_ReturnsBody(t *testing.T) {
	t.Parallel()
	skipIfNoGit(t)
	dir := t.TempDir()
	initGitRepo(t, dir)

	relPath := filepath.Join("investigation", "show-me.yaml")
	// Note: runGit trims trailing whitespace, so gitShowAtCommit returns the
	// body without a trailing newline. Compare against the trimmed form.
	v1 := "id: show-me\nversion: 1"
	v2 := "id: show-me\nversion: 2"

	writeAndCommit(t, dir, relPath, v1+"\n", "show-me: v1")
	sha1 := gitHead(t, dir)

	writeAndCommit(t, dir, relPath, v2+"\n", "show-me: v2")

	got, err := gitShowAtCommit(context.Background(), dir, sha1, relPath)
	require.NoError(t, err)
	assert.Equal(t, v1, got, "should return the body at the older commit")
}

// TestGitShowAtCommit_MissingCommit returns an error for an unknown sha.
func TestGitShowAtCommit_MissingCommit(t *testing.T) {
	t.Parallel()
	skipIfNoGit(t)
	dir := t.TempDir()
	initGitRepo(t, dir)

	_, err := gitShowAtCommit(context.Background(), dir, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "some.yaml")
	assert.Error(t, err, "unknown sha must return an error")
}
