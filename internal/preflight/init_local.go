package preflight

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// initEmptyLocal creates dir (if missing) and runs `git init` inside it
// so the launcher has a usable local-only checkout when no upstream repo
// is configured. Downstream code paths that `git add` / `git commit`
// playbook, wiki, or session edits rely on a `.git` entry; without one,
// every save would fail.
//
// Idempotent at the dir level: callers are expected to only invoke this
// after checking that the dir is missing or empty, so we never overwrite
// pre-existing content.
func initEmptyLocal(ctx context.Context, dir string) error {
	if dir == "" {
		return fmt.Errorf("init local checkout: dir is empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(initCtx, "git", "init", "-q", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git init %s: %w\n%s", dir, err, strings.TrimSpace(string(out)))
	}
	return nil
}

