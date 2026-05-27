package preflight

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// SessionsCloneOpts configures the boot-time upstream sessions clone.
//
//   - Repo: GitHub OWNER/REPO. The clone source URL is derived as
//     https://github.com/<repo>.git.
//   - Dir: filesystem path to clone into (or to verify pre-populated).
//     The launcher creates the parent dir if missing.
//   - Offline: don't run `git clone` even if Dir is empty. Used for
//     air-gapped environments and dev workflows where the operator
//     pre-seeds the dir. Sourced from profile.Defaults.Offline.
type SessionsCloneOpts struct {
	Repo    string
	Dir     string
	Offline bool
}

// EnsureSessionsClone idempotently makes sure the upstream sessions
// repo is cloned at the configured path. Mirrors EnsureWikiClone:
//
//  1. Dir is non-empty AND looks like a git checkout → no-op (just
//     return). The launcher does NOT auto-pull on every start; the
//     operator triggers sync explicitly via the upstream-sessions
//     header on /.
//  2. Dir is missing OR empty AND Offline is false → run
//     `git clone <repo> <dir>`. Hard-fails on network / auth /
//     repo-doesn't-exist; the launcher refuses to start with a clear
//     error message rather than ship a stale fallback.
//  3. Dir is missing OR empty AND Offline is true → return a clear
//     error pointing at the dir so the operator can populate it
//     manually (used in air-gapped envs).
//
// Bounded clone timeout (60s) so a hung network doesn't block startup.
func EnsureSessionsClone(ctx context.Context, opts SessionsCloneOpts) error {
	if opts.Dir == "" {
		return fmt.Errorf("sessions dir is empty")
	}
	if err := os.MkdirAll(filepath.Dir(opts.Dir), 0o700); err != nil {
		return fmt.Errorf("create parent of %s: %w", opts.Dir, err)
	}
	exists, gitInside, err := sessionsDirState(opts.Dir)
	if err != nil {
		return err
	}
	if exists && gitInside {
		return nil
	}
	if exists && !gitInside {
		// Dir exists with content but isn't a git repo. Could be a
		// manual seed or leftover state — accept silently. Sync won't
		// work but loading will.
		return nil
	}
	if opts.Repo == "" {
		// No upstream configured — degrade to a local-only checkout so
		// session draft commits land somewhere. The "push session as
		// PR" surface is gated on opts.Repo being set.
		return initEmptyLocal(ctx, opts.Dir)
	}
	if opts.Offline {
		return fmt.Errorf("sessions dir %s is empty and the profile is set to offline; populate it manually (e.g. `git clone https://github.com/%s.git %s`) or set defaults.offline=false in the profile", opts.Dir, opts.Repo, opts.Dir)
	}
	_ = os.Remove(opts.Dir)
	cloneURL := "https://github.com/" + opts.Repo + ".git"
	cloneCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cloneCtx, "git", "clone", "--depth=1", cloneURL, opts.Dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("clone upstream sessions repo %s into %s: %w\ngit output:\n%s", cloneURL, opts.Dir, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// sessionsDirState reports whether the dir exists, and whether it looks
// like a git checkout (has a .git entry, file or dir — submodule-style
// .git files count). Empty + present dirs are treated as
// not-yet-cloned so the caller falls through to the clone branch.
func sessionsDirState(dir string) (exists, isGitCheckout bool, err error) {
	stat, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("stat %s: %w", dir, err)
	}
	if !stat.IsDir() {
		return false, false, fmt.Errorf("%s exists but is not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true, false, fmt.Errorf("read %s: %w", dir, err)
	}
	if len(entries) == 0 {
		return false, false, nil
	}
	for _, e := range entries {
		if e.Name() == ".git" {
			return true, true, nil
		}
	}
	return true, false, nil
}
