package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// resolveRefFixture builds a Server with a fixture clone that mimics the
// cache layout EnsureClone produces: a real repo with origin/<branch>
// remote-tracking refs and a tag. The fixture has these refs available:
//
//   - HEAD on local main pointing at C1
//   - origin/main pointing at C1
//   - origin/feature/foo pointing at C2 (no local feature/foo branch)
//   - tag v0.1.0 pointing at C0 (annotated, kept distinct from any branch)
//
// resolveRef probes against the fixture's actual git refs, so this is a
// behavioural test, not a mock-based one.
func resolveRefFixture(t *testing.T) (s *Server, repoDir string) {
	t.Helper()
	// "Upstream" bare repo — origin from the cache clone's perspective.
	bare := filepath.Join(t.TempDir(), "upstream.git")
	runFixtureGit(t, "", "init", "--bare", "--initial-branch=main", bare)

	// Author repo that pushes to bare. We need this because git refuses
	// to push into a bare repo's checked-out branch from a non-bare repo
	// directly without a working tree.
	authorDir := filepath.Join(t.TempDir(), "author")
	runFixtureGit(t, "", "init", "--initial-branch=main", authorDir)
	runFixtureGit(t, authorDir, "config", "commit.gpgsign", "false")
	runFixtureGit(t, authorDir, "config", "tag.gpgsign", "false")
	runFixtureGit(t, authorDir, "remote", "add", "origin", bare)
	writeFile(t, authorDir, "README.md", "v0\n")
	runFixtureGit(t, authorDir, "add", "README.md")
	runFixtureGit(t, authorDir, "commit", "-q", "-m", "c0")
	runFixtureGit(t, authorDir, "tag", "-a", "-m", "v0.1.0", "v0.1.0")
	writeFile(t, authorDir, "README.md", "v1\n")
	runFixtureGit(t, authorDir, "add", "README.md")
	runFixtureGit(t, authorDir, "commit", "-q", "-m", "commit-1")
	runFixtureGit(t, authorDir, "push", "-u", "origin", "main")
	runFixtureGit(t, authorDir, "push", "origin", "v0.1.0")
	// Push a feature branch with one more commit, then go back to main.
	runFixtureGit(t, authorDir, "checkout", "-b", "feature/foo")
	writeFile(t, authorDir, "README.md", "v2-feature\n")
	runFixtureGit(t, authorDir, "add", "README.md")
	runFixtureGit(t, authorDir, "commit", "-q", "-m", "c2")
	runFixtureGit(t, authorDir, "push", "-u", "origin", "feature/foo")

	// Cache clone — what EnsureClone would have built. Clone the bare to
	// a path that matches what `cacheDir/<owner>/<name>` expects.
	cacheDir := t.TempDir()
	repoDir = filepath.Join(cacheDir, "o", "n")
	runFixtureGit(t, "", "clone", "--filter=blob:none", bare, repoDir)
	runFixtureGit(t, repoDir, "config", "commit.gpgsign", "false")

	s = &Server{owner: "o", name: "n", cacheDir: cacheDir, gh: &stubGh{response: []byte("main\n")}}
	return s, repoDir
}

func writeFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
}

func TestResolveRef_EmptyResolvesToOriginDefault(t *testing.T) {
	t.Parallel()
	s, repoDir := resolveRefFixture(t)
	got, err := s.resolveRef(context.Background(), repoDir, "")
	require.NoError(t, err)
	require.Equal(t, "origin/main", got)
}

func TestResolveRef_DefaultBranchNameRewritesToOrigin(t *testing.T) {
	t.Parallel()
	s, repoDir := resolveRefFixture(t)
	got, err := s.resolveRef(context.Background(), repoDir, "main")
	require.NoError(t, err)
	require.Equal(t, "origin/main", got)
}

func TestResolveRef_FeatureBranchRewritesToOrigin(t *testing.T) {
	t.Parallel()
	s, repoDir := resolveRefFixture(t)
	got, err := s.resolveRef(context.Background(), repoDir, "feature/foo")
	require.NoError(t, err)
	require.Equal(t, "origin/feature/foo", got, "any branch with a remote-tracking ref must be rewritten")
}

func TestResolveRef_TagPassesThrough(t *testing.T) {
	t.Parallel()
	s, repoDir := resolveRefFixture(t)
	got, err := s.resolveRef(context.Background(), repoDir, "v0.1.0")
	require.NoError(t, err)
	require.Equal(t, "v0.1.0", got, "tags are immutable — no rewrite")
}

func TestResolveRef_AlreadyQualifiedPassesThrough(t *testing.T) {
	t.Parallel()
	s, repoDir := resolveRefFixture(t)
	got, err := s.resolveRef(context.Background(), repoDir, "origin/main")
	require.NoError(t, err)
	require.Equal(t, "origin/main", got)

	got2, err := s.resolveRef(context.Background(), repoDir, "refs/tags/v0.1.0")
	require.NoError(t, err)
	require.Equal(t, "refs/tags/v0.1.0", got2)
}

func TestResolveRef_UnknownBareNamePassesThrough(t *testing.T) {
	t.Parallel()
	s, repoDir := resolveRefFixture(t)
	// A name with no remote-tracking ref and not in /refs/. Could be a SHA,
	// a typo, or a tag without an annotated object. Pass through so the
	// downstream git command surfaces the agent's input verbatim in its
	// error.
	got, err := s.resolveRef(context.Background(), repoDir, "nope-not-real")
	require.NoError(t, err)
	require.Equal(t, "nope-not-real", got)
}

func TestResolveRef_ShortShaPassesThrough(t *testing.T) {
	t.Parallel()
	s, repoDir := resolveRefFixture(t)
	// Grab a real SHA from the fixture's HEAD.
	sha, err := gitOutput(context.Background(), repoDir, "rev-parse", "HEAD")
	require.NoError(t, err)
	short := sha[:7]
	got, err := s.resolveRef(context.Background(), repoDir, short)
	require.NoError(t, err)
	require.Equal(t, short, got)
}
