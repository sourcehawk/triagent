//go:build e2e

package harness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// buildLocalClone materializes a hermetic git clone for the repos fixture.
// It seeds a bare origin from seedDir's contents, then clones it into
// clonePath so the launcher's EnsureClone sees a `.git` (cache hit) and its
// `git fetch --prune --tags` against the local-file origin succeeds with no
// network. The sub-agent the regenerate worker spawns runs in clonePath.
//
// seedDir holds the working-tree files the test wants present in the clone
// (a README, source stubs the summary prompt would read). All git identity
// is pinned via -c flags so the build doesn't depend on the runner's global
// git config.
func buildLocalClone(seedDir, originPath, clonePath string) error {
	if err := os.MkdirAll(filepath.Dir(originPath), 0o700); err != nil {
		return fmt.Errorf("mkdir origin parent: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(clonePath), 0o700); err != nil {
		return fmt.Errorf("mkdir clone parent: %w", err)
	}

	// A scratch working tree we commit from, then push into the bare origin.
	work, err := os.MkdirTemp("", "triagent-e2e-seed-work-")
	if err != nil {
		return fmt.Errorf("mktemp work: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	if err := copyTree(seedDir, work); err != nil {
		return fmt.Errorf("copy seed tree: %w", err)
	}

	if err := git("", "init", "--bare", "--initial-branch=main", originPath); err != nil {
		return err
	}
	if err := git(work, "init", "--initial-branch=main"); err != nil {
		return err
	}
	if err := git(work, "add", "-A"); err != nil {
		return err
	}
	if err := gitCommit(work, "seed fixture clone"); err != nil {
		return err
	}
	if err := git(work, "remote", "add", "origin", originPath); err != nil {
		return err
	}
	if err := git(work, "push", "origin", "main"); err != nil {
		return err
	}
	// Clone from the local origin so the resulting repo has origin wired and
	// `git fetch` works offline — exactly what EnsureClone runs on cache hit.
	if err := git("", "clone", originPath, clonePath); err != nil {
		return err
	}
	return nil
}

// git runs a git subcommand in dir (empty dir = inherit cwd), with identity
// and prompts pinned so it never reads the runner's global config or blocks
// on credentials. Output is captured and surfaced on failure.
func git(dir string, args ...string) error {
	base := []string{
		"-c", "user.name=triagent-e2e",
		"-c", "user.email=e2e@triagent.test",
		"-c", "init.defaultBranch=main",
		"-c", "commit.gpgsign=false",
		"-c", "advice.detachedHead=false",
	}
	cmd := exec.Command("git", append(base, args...)...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %w\n%s", args, err, out)
	}
	return nil
}

// gitCommit commits the staged tree with --allow-empty so a seed dir with
// only ignored content still produces a HEAD to push.
func gitCommit(dir, msg string) error {
	return git(dir, "commit", "--allow-empty", "-m", msg)
}
