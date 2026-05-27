package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHasGitInAncestry(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "wikis"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "a", "b"), 0o700))

	// Flat layout: workdir = root, subpath = "" → .git lives at root.
	require.True(t, hasGitInAncestry(root, ""))

	// Single-segment subpath layout: workdir = root/wikis, subpath = "wikis"
	// → no .git at workdir, but parent (= root) has it.
	require.True(t, hasGitInAncestry(filepath.Join(root, "wikis"), "wikis"))

	// Two-segment subpath layout: workdir = root/a/b, subpath = "a/b"
	// → walk up twice to find .git at root.
	require.True(t, hasGitInAncestry(filepath.Join(root, "a", "b"), "a/b"))

	// Negative: workdir under a non-git tree, no .git up any ancestor
	// within `subpath` worth of segments.
	other := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(other, "wikis"), 0o700))
	require.False(t, hasGitInAncestry(filepath.Join(other, "wikis"), "wikis"))

	// Empty subpath but workdir lacks .git → no ancestor walk, false.
	require.False(t, hasGitInAncestry(other, ""))
}

func TestCheckRepoPath_AcceptsCloneRootViaAncestry(t *testing.T) {
	t.Parallel()
	// Mirror the default-profile layout: clone root has .git, work-dir is
	// the <subpath> dir underneath. The old checkRepoPath stat'd
	// <workdir>/.git directly and failed; the fix walks ancestry.
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "playbooks"), 0o700))

	got := checkRepoPath(filepath.Join(root, "playbooks"), "owner/repo", "playbooks")
	require.True(t, got.Configured)
	require.True(t, got.Valid, "expected ancestry walk to find .git at clone root; reason=%q", got.Reason)
	require.Equal(t, "owner/repo", got.Repo)
	require.Equal(t, "playbooks", got.Subpath)
}

func TestCheckRepoPath_FlatLayoutStillWorks(t *testing.T) {
	t.Parallel()
	// Operator points --repo-path directly at the repo root (no subpath).
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o700))

	got := checkRepoPath(root, "owner/repo", "")
	require.True(t, got.Configured)
	require.True(t, got.Valid)
}

func TestCheckRepoPath_NoGitAnywhereInvalid(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "playbooks"), 0o700))

	got := checkRepoPath(filepath.Join(root, "playbooks"), "owner/repo", "playbooks")
	require.True(t, got.Configured)
	require.False(t, got.Valid)
	require.Contains(t, got.Reason, "no .git entry")
}

func TestCodefixCapability_GatesOnGhAndLinkedRepos(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		ghAuthed       bool
		hasLinkedRepos bool
		wantEnabled    bool
		wantReason     string
	}{
		{"gh + repos", true, true, true, ""},
		{"no gh", false, true, false, "GitHub CLI not authenticated"},
		{"no repos", true, false, false, "No repos linked to this investigation"},
		{"neither", false, false, false, "GitHub CLI not authenticated"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := computeCodefixCapability(c.ghAuthed, c.hasLinkedRepos)
			require.Equal(t, c.wantEnabled, got.Enabled)
			require.Equal(t, c.ghAuthed, got.GhAuthenticated)
			require.Equal(t, c.hasLinkedRepos, got.HasLinkedRepos)
			if c.wantReason == "" {
				require.Empty(t, got.Reason)
			} else {
				require.Contains(t, got.Reason, c.wantReason)
			}
		})
	}
}
