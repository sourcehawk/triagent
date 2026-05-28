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
func seedFixtures(stateDir string, opts Options) (string, error) {
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
		{opts.RepoFixtures, "repos", filepath.Join(base, "repos")},
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
	return profilePath, nil
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
