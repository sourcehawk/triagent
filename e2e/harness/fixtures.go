//go:build e2e

package harness

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// fixturesRoot resolves the e2e/fixtures directory from this source file's
// location, so fixture lookups work regardless of the test's working dir.
func fixturesRoot() string {
	_, file, _, _ := runtime.Caller(0)
	// file is e2e/harness/fixtures.go → up two to e2e/, then fixtures/.
	return filepath.Join(filepath.Dir(filepath.Dir(file)), "fixtures")
}

// fixtureDir returns the absolute path to fixtures/<bucket>/<scenario>.
func fixtureDir(bucket, scenario string) string {
	return filepath.Join(fixturesRoot(), bucket, scenario)
}

// seedFixtures resolves the profile path and copies any requested fixture
// scenarios into the launcher's per-profile state dir under stateDir. It
// returns the absolute profile path to pass as --profile.
//
// The per-profile bucket layout mirrors profile.defaultPathTemplates:
// ${XDG_CONFIG_HOME}/triagent/<profile>/<bucket>. The harness writes
// fixtures there directly so the launcher discovers them the normal way.
//
// cacheDir is the launcher's XDG_CACHE_HOME root. The repos vault is split
// across both roots — user_repos.yaml lives under the config dir, but the
// summary cache and the git clones live under
// ${XDG_CACHE_HOME}/triagent-mcp/<profile>/git — so the repos scenario is
// seeded by seedRepoVault rather than a flat bucket copy.
func seedFixtures(stateDir, cacheDir string, opts Options) (string, error) {
	if opts.Profile == "" {
		return "", fmt.Errorf("Options.Profile is required")
	}
	profilePath := fixtureDir("profiles", opts.Profile)
	if _, err := os.Stat(filepath.Join(profilePath, "profile.yaml")); err != nil {
		return "", fmt.Errorf("fixture profile %q: %w", opts.Profile, err)
	}

	base := filepath.Join(stateDir, "triagent", opts.Profile)

	type seed struct {
		scenario string
		bucket   string // source fixtures/<bucket>
		dest     string // destination under the per-profile state dir
	}
	seeds := []seed{
		{opts.SessionFixtures, "sessions", filepath.Join(base, "sessions")},
		{opts.PlaybookFixtures, "playbooks", filepath.Join(base, "playbooks")},
		{opts.WikiFixtures, "wiki", filepath.Join(base, "wiki")},
	}
	for _, s := range seeds {
		if s.scenario == "" {
			continue
		}
		src := fixtureDir(s.bucket, s.scenario)
		if _, err := os.Stat(src); err != nil {
			return "", fmt.Errorf("fixture %s/%s: %w", s.bucket, s.scenario, err)
		}
		if err := copyTree(src, s.dest); err != nil {
			return "", fmt.Errorf("seed %s/%s: %w", s.bucket, s.scenario, err)
		}
	}

	if opts.RepoFixtures != "" {
		gitCacheDir := filepath.Join(cacheDir, "triagent-mcp", opts.Profile, "git")
		if err := seedRepoVault(fixtureDir("repos", opts.RepoFixtures), base, gitCacheDir); err != nil {
			return "", fmt.Errorf("seed repos/%s: %w", opts.RepoFixtures, err)
		}
	}
	return profilePath, nil
}

// seedRepoVault lays a repos scenario across the two roots the launcher
// reads from. The scenario dir contains, all optional:
//
//   - user_repos.yaml          → <configBase>/user_repos.yaml (the
//     user-local repos the page lists under "yours").
//   - summaries/<o>/<n>/…       → <gitCacheDir>/summaries/<o>/<n>/… (the
//     repo-summary vault GET .../summary reads; mirrors repos.SummaryPath).
//   - clones/<o>/<n>/           → a real git clone at <gitCacheDir>/<o>/<n>
//     with a local-file origin, so the regenerate worker's EnsureClone
//     (git fetch against origin) succeeds offline and the summary
//     sub-agent has a working tree to run in.
func seedRepoVault(scenarioDir, configBase, gitCacheDir string) error {
	if _, err := os.Stat(scenarioDir); err != nil {
		return fmt.Errorf("scenario dir: %w", err)
	}

	if src := filepath.Join(scenarioDir, "user_repos.yaml"); fileExists(src) {
		if err := copyFileAt(src, filepath.Join(configBase, "user_repos.yaml")); err != nil {
			return fmt.Errorf("user_repos.yaml: %w", err)
		}
	}

	if summaries := filepath.Join(scenarioDir, "summaries"); dirExists(summaries) {
		if err := copyTree(summaries, filepath.Join(gitCacheDir, "summaries")); err != nil {
			return fmt.Errorf("summaries: %w", err)
		}
	}

	if clones := filepath.Join(scenarioDir, "clones"); dirExists(clones) {
		if err := seedClones(clones, gitCacheDir); err != nil {
			return fmt.Errorf("clones: %w", err)
		}
	}
	return nil
}

// seedClones turns each clones/<owner>/<name> seed dir into a real git
// clone at gitCacheDir/<owner>/<name> whose origin is a local bare repo.
// EnsureClone treats the presence of a `.git` dir as a cache hit and runs
// `git fetch --prune --tags`, which against a local-file origin succeeds
// with no network — keeping the regenerate flow hermetic.
func seedClones(clonesRoot, gitCacheDir string) error {
	entries, err := readOwnerRepoDirs(clonesRoot)
	if err != nil {
		return err
	}
	for _, e := range entries {
		seedDir := filepath.Join(clonesRoot, e.owner, e.name)
		origin := filepath.Join(gitCacheDir, ".origins", e.owner, e.name+".git")
		clone := filepath.Join(gitCacheDir, e.owner, e.name)
		if err := buildLocalClone(seedDir, origin, clone); err != nil {
			return fmt.Errorf("%s/%s: %w", e.owner, e.name, err)
		}
	}
	return nil
}

// ownerRepo is one owner/name pair discovered under a two-level dir tree.
type ownerRepo struct{ owner, name string }

// readOwnerRepoDirs lists every <owner>/<name> directory pair under root.
func readOwnerRepoDirs(root string) ([]ownerRepo, error) {
	owners, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []ownerRepo
	for _, o := range owners {
		if !o.IsDir() {
			continue
		}
		names, err := os.ReadDir(filepath.Join(root, o.Name()))
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			if n.IsDir() {
				out = append(out, ownerRepo{owner: o.Name(), name: n.Name()})
			}
		}
	}
	return out, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// copyFileAt copies src to dst, creating dst's parent dir. Mode is the
// source's permission bits.
func copyFileAt(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	return copyFile(src, dst, info.Mode())
}

// copyTree recursively copies src into dst, creating dst. File modes are
// preserved within the user rwx bits; the suite never needs setuid/sticky.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
