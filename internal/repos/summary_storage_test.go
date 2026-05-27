package repos

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultGitCacheDir_PrefersXDGCacheHome(t *testing.T) {
	// Cannot t.Parallel — t.Setenv mutates process-global env.
	t.Setenv("XDG_CACHE_HOME", "/tmp/xdg-cache")
	got, err := DefaultGitCacheDir()
	require.NoError(t, err)
	assert.Equal(t, "/tmp/xdg-cache/triagent-mcp/git", got)
}

func TestDefaultGitCacheDir_FallsBackToHome(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "/tmp/fake-home")
	got, err := DefaultGitCacheDir()
	require.NoError(t, err)
	assert.Equal(t, "/tmp/fake-home/.cache/triagent-mcp/git", got,
		"missing XDG_CACHE_HOME → ~/.cache/triagent-mcp/git, matching mcp/internal/git.defaultCacheDir")
}

func TestBaselinePath_LayoutMatchesActiveSibling(t *testing.T) {
	t.Parallel()
	active := SummaryPath("/cache", "example-org", "example-app")
	baseline := BaselinePath("/cache", "example-org", "example-app")
	assert.Equal(t, filepath.Dir(active), filepath.Dir(baseline),
		"baseline must live next to the active cache file")
	assert.NotEqual(t, active, baseline)
	assert.True(t, strings.HasSuffix(baseline, "architecture_summary.baseline.md"),
		"unexpected baseline filename: %s", baseline)
	assert.Contains(t, active, filepath.Join("summaries", "example-org", "example-app"),
		"summaries must live under <cacheDir>/summaries/<owner>/<name> — keeps them out of the clone working tree at <cacheDir>/<owner>/<name>")
}

func TestReadBaselineBody_NotFound(t *testing.T) {
	t.Parallel()
	_, err := ReadBaselineBody(filepath.Join(t.TempDir(), "missing.md"))
	assert.ErrorIs(t, err, ErrBaselineNotFound)
}

func TestWriteAndReadBaselineBody_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := BaselinePath(dir, "example-org", "example-app")
	body := "# example-org/example-app — architecture summary\n\n## Top-level structure\n\nfoo.\n"
	require.NoError(t, WriteBaselineBody(path, body))
	got, err := ReadBaselineBody(path)
	require.NoError(t, err)
	assert.Equal(t, body, got)
}

func TestOperatorEditsDiff_NoBaseline_NoEdits(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	diff, has, err := OperatorEditsDiff(context.Background(),
		BaselinePath(dir, "example-org", "example-app"),
		"# something\n",
	)
	require.NoError(t, err)
	assert.False(t, has)
	assert.Empty(t, diff)
}

func TestOperatorEditsDiff_BaselineMatchesActive_NoEdits(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	body := "# match\n\nthe same\n"
	path := BaselinePath(dir, "example-org", "example-app")
	require.NoError(t, WriteBaselineBody(path, body))
	diff, has, err := OperatorEditsDiff(context.Background(), path, body)
	require.NoError(t, err)
	assert.False(t, has, "identical bodies must report no edits")
	assert.Empty(t, diff)
}

func TestOperatorEditsDiff_DivergedActive_ReturnsUnifiedDiff(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	baseline := "# h1\n\n## section\n\nline A\nline B\nline C\n"
	active := "# h1\n\n## section\n\nline A\noperator-added line\nline B\nline C\n"
	path := BaselinePath(dir, "example-org", "example-app")
	require.NoError(t, WriteBaselineBody(path, baseline))
	diff, has, err := OperatorEditsDiff(context.Background(), path, active)
	require.NoError(t, err)
	assert.True(t, has, "diverged bodies must report edits")
	assert.Contains(t, diff, "+operator-added line",
		"unified diff must include the inserted line")
	assert.Contains(t, diff, "@@",
		"output must be a unified diff, not a side-by-side or summary")
}
