package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// initFixtureRepo creates a real git repo in t.TempDir() with two commits
// and a tag, returns the absolute path. Discovery tools run against this
// directly (no clone) since EnsureClone is bypassed by pre-creating the
// .git directory at the cache location.
func initFixtureRepo(t *testing.T, owner, name string) (cacheDir, repoDir string) {
	t.Helper()
	cacheDir = t.TempDir()
	repoDir = filepath.Join(cacheDir, owner, name)
	require.NoError(t, os.MkdirAll(repoDir, 0o700), "mkdir")
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		// Disable any global hooks / GPG signing that might be on by default.
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
			"GIT_CONFIG_NOSYSTEM=1",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	writeFile := func(rel, contents string) {
		full := filepath.Join(repoDir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o700), "mkdir")
		require.NoError(t, os.WriteFile(full, []byte(contents), 0o600), "write")
	}
	run("init", "-q", "--initial-branch=main")
	run("config", "commit.gpgsign", "false")
	run("config", "tag.gpgsign", "false")

	writeFile("README.md", "hello\n")
	run("add", "README.md")
	run("commit", "-q", "-m", "initial commit")
	run("tag", "-a", "-m", "first release", "v0.1.0")

	// Force distinguishable committerdate between the two tagged commits
	// so order-by-date tests aren't sensitive to sub-second timing.
	time.Sleep(1100 * time.Millisecond)

	writeFile("README.md", "hello world\n")
	writeFile("src/foo.go", "package foo\n")
	run("add", "-A")
	run("commit", "-q", "-m", "fix: reduce backpressure timeout")
	run("tag", "-a", "-m", "second release", "v0.2.0")
	return cacheDir, repoDir
}

// fixtureServer builds a Server pointing at the fixture repo. It bypasses
// EnsureClone by relying on the pre-populated cache layout.
func fixtureServer(t *testing.T, owner, name string) *Server {
	t.Helper()
	cacheDir, _ := initFixtureRepo(t, owner, name)
	s, err := New(Options{Repo: owner + "/" + name, CacheDir: cacheDir})
	require.NoError(t, err, "new")
	return s
}

func TestLatestTags_ReturnsTagsInOrder(t *testing.T) {
	t.Parallel()
	s := fixtureServer(t, "example-org", "fixture")
	ctx := context.Background()
	_, out, err := s.latestTags(ctx, nil, latestTagsIn{Limit: 5})
	require.NoError(t, err, "latestTags")
	require.Len(t, out.Tags, 2)
	// Newest first.
	assert.Equal(t, "v0.2.0", out.Tags[0].Name, "expected v0.2.0 first")
	assert.Contains(t, out.Tags[0].Subject, "backpressure", "expected commit subject in tag")
}

func TestCommitSummary_RefIsRequired(t *testing.T) {
	t.Parallel()
	s := fixtureServer(t, "example-org", "fixture")
	res, _, _ := s.commitSummary(context.Background(), nil, commitSummaryIn{})
	require.NotNil(t, res)
	assert.True(t, res.IsError, "expected IsError when ref missing")
}

func TestCommitSummary_PopulatesFieldsForKnownRef(t *testing.T) {
	t.Parallel()
	s := fixtureServer(t, "example-org", "fixture")
	_, out, err := s.commitSummary(context.Background(), nil, commitSummaryIn{Ref: "v0.2.0"})
	require.NoError(t, err, "commitSummary")
	assert.Contains(t, out.Subject, "backpressure", "subject mismatch")
	assert.NotZero(t, out.Stats.Files, "expected file stats")
	assert.NotEmpty(t, out.Files, "expected files list")
}

func TestSearchLog_FindsBySubject(t *testing.T) {
	t.Parallel()
	s := fixtureServer(t, "example-org", "fixture")
	_, out, err := s.searchLog(context.Background(), nil, searchLogIn{Grep: "backpressure", Since: "10 years"})
	require.NoError(t, err, "searchLog")
	require.Len(t, out.Matches, 1)
	assert.Contains(t, out.Matches[0].Subject, "backpressure", "match subject mismatch")
}

func TestDiffSummary_ReportsCommitsBetweenTags(t *testing.T) {
	t.Parallel()
	s := fixtureServer(t, "example-org", "fixture")
	_, out, err := s.diffSummary(context.Background(), nil, diffSummaryIn{From: "v0.1.0", To: "v0.2.0"})
	require.NoError(t, err, "diffSummary")
	require.Len(t, out.Commits, 1, "expected 1 commit between tags")
	assert.Contains(t, out.Commits[0].Subject, "backpressure", "commit subject mismatch")
	assert.NotEmpty(t, out.TopFiles, "expected file churn")
}

func TestIsFilteredPrereleaseTag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tag      string
		filtered bool
	}{
		// Stable releases — kept.
		{"1.2.3", false},
		{"v1.2.3", false},
		{"8.7.0", false},
		{"v8.7.0", false},
		// SNAPSHOT bypass — kept regardless of case.
		{"1.2.3-SNAPSHOT", false},
		{"1.2.3-snapshot", false},
		{"v8.8.0-SNAPSHOT", false},
		// Prereleases the filter catches.
		{"1.2.3-rc1", true},
		{"1.2.3-alpha", true},
		{"1.2.3-alpha.1", true},
		{"1.2.3-beta+build.42", true},
		{"v8.7.0-rc1", true},
		{"1.2.3-mytesttag", true},
		// Non-semver tags — kept (filter only catches semver-shaped).
		{"nightly-20260101", false},
		{"release-2026-05-06", false},
		{"version-8-pinned", false},
		{"main", false},
		// Edge: incomplete cores are not semver, so kept.
		{"1.2-rc1", false},
		{"v1-rc1", false},
	}
	for _, c := range cases {
		got := isFilteredPrereleaseTag(c.tag)
		assert.Equal(t, c.filtered, got, "isFilteredPrereleaseTag(%q)", c.tag)
	}
}

func TestLatestTags_FiltersPrereleasesByDefault(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	owner, name := "example-org", "prerelease-fixture"
	repoDir := filepath.Join(cacheDir, owner, name)
	require.NoError(t, os.MkdirAll(repoDir, 0o700), "mkdir")
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
			"GIT_CONFIG_NOSYSTEM=1",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	write := func(rel, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(repoDir, rel), []byte(body), 0o600), "write")
	}
	run("init", "-q", "--initial-branch=main")
	run("config", "commit.gpgsign", "false")
	run("config", "tag.gpgsign", "false")
	write("README.md", "a\n")
	run("add", "README.md")
	run("commit", "-q", "-m", "release 1.2.3")
	run("tag", "1.2.3")
	run("tag", "1.2.3-rc1")
	run("tag", "1.2.3-mytesttag")
	run("tag", "1.2.3-SNAPSHOT")
	run("tag", "nightly-20260101") // non-semver — should never be filtered

	// Filter on (default).
	on, err := New(Options{Repo: owner + "/" + name, CacheDir: cacheDir, FilterPrereleases: true})
	require.NoError(t, err, "new")
	_, out, err := on.latestTags(context.Background(), nil, latestTagsIn{Limit: 10})
	require.NoError(t, err, "latestTags")
	got := tagNameSet(out.Tags)
	for _, kept := range []string{"1.2.3", "1.2.3-SNAPSHOT", "nightly-20260101"} {
		_, ok := got[kept]
		assert.True(t, ok, "expected %q in filtered output, got %v", kept, got)
	}
	for _, dropped := range []string{"1.2.3-rc1", "1.2.3-mytesttag"} {
		_, ok := got[dropped]
		assert.False(t, ok, "expected %q to be filtered, but it appeared", dropped)
	}

	// Per-call override (include_prereleases=true) returns everything.
	_, allOut, err := on.latestTags(context.Background(), nil, latestTagsIn{Limit: 10, IncludePrereleases: true})
	require.NoError(t, err, "latestTags include_prereleases")
	all := tagNameSet(allOut.Tags)
	for _, want := range []string{"1.2.3", "1.2.3-rc1", "1.2.3-mytesttag", "1.2.3-SNAPSHOT", "nightly-20260101"} {
		_, ok := all[want]
		assert.True(t, ok, "expected %q with override, got %v", want, all)
	}

	// Filter off at server level returns everything by default too.
	off, err := New(Options{Repo: owner + "/" + name, CacheDir: cacheDir, FilterPrereleases: false})
	require.NoError(t, err, "new (off)")
	_, offOut, err := off.latestTags(context.Background(), nil, latestTagsIn{Limit: 10})
	require.NoError(t, err, "latestTags (off)")
	assert.Len(t, offOut.Tags, len(all), "server-level filter off should return everything")
}

func tagNameSet(tags []tagInfo) map[string]struct{} {
	out := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		out[t.Name] = struct{}{}
	}
	return out
}

func TestParseShortstat(t *testing.T) {
	t.Parallel()
	s := parseShortstat(" 3 files changed, 12 insertions(+), 5 deletions(-)")
	assert.Equal(t, 3, s.Files)
	assert.Equal(t, 12, s.Insertions)
	assert.Equal(t, 5, s.Deletions)
	// Insert-only line.
	s = parseShortstat(" 1 file changed, 7 insertions(+)")
	assert.Equal(t, 1, s.Files)
	assert.Equal(t, 7, s.Insertions)
	assert.Equal(t, 0, s.Deletions)
}
