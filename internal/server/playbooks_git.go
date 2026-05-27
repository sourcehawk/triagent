package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// gitInitUserPlaybooksDir ensures dir is a git repository so save +
// rollback operations have somewhere to commit. Idempotent: a no-op
// when .git already exists. Best-effort: failures log a warning and
// return nil (history dropdown degrades gracefully when the repo is
// missing).
//
// Configures user.name / user.email locally so commits don't fail on
// systems without a global git identity. The committer is decorative
// — saves carry the operator's intent in the body, not the author.
func gitInitUserPlaybooksDir(ctx context.Context, dir string) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create user playbooks dir: %w", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return nil // already a repo
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat .git: %w", err)
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if out, err := runGit(cmdCtx, dir, "init", "--initial-branch=main"); err != nil {
		fmt.Fprintf(os.Stderr, "investigate: git init %s failed: %v (%s) — history features will be unavailable\n", dir, err, out)
		return nil
	}
	if out, err := runGit(cmdCtx, dir, "config", "user.name", "triagent"); err != nil {
		fmt.Fprintf(os.Stderr, "investigate: git config user.name failed: %v (%s)\n", err, out)
	}
	if out, err := runGit(cmdCtx, dir, "config", "user.email", "noreply@triagent.local"); err != nil {
		fmt.Fprintf(os.Stderr, "investigate: git config user.email failed: %v (%s)\n", err, out)
	}
	// Stage and commit anything the operator already had on disk so
	// the first Save lands on top of a real history root.
	if out, err := runGit(cmdCtx, dir, "add", "-A"); err != nil {
		fmt.Fprintf(os.Stderr, "investigate: git add -A in %s failed: %v (%s) — initial commit will be empty\n", dir, err, out)
	} else if out, err := runGit(cmdCtx, dir, "commit", "--allow-empty", "-m", "initial: user playbooks dir"); err != nil {
		fmt.Fprintf(os.Stderr, "investigate: initial commit in %s failed: %v (%s)\n", dir, err, out)
	}
	return nil
}

// playbookCommit is one entry returned by the commits API.
type playbookCommit struct {
	Sha     string `json:"sha"`
	Summary string `json:"summary"`
	Date    string `json:"date"`   // RFC3339
	Source  string `json:"source"` // "local" | "upstream"
}

// gitLogForPlaybook returns up to limit commits touching relPath
// in dir, newest first. before is an optional cutoff sha (commits older
// than it). source labels each entry.
func gitLogForPlaybook(ctx context.Context, dir, relPath, source, before string, limit int) ([]playbookCommit, error) {
	if dir == "" || limit <= 0 {
		return nil, nil
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return nil, nil // no repo — empty history is fine
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := []string{"log", fmt.Sprintf("--max-count=%d", limit), "--format=%H%x09%cI%x09%s"}
	if before != "" {
		args = append(args, before+"~1")
	}
	args = append(args, "--", relPath)
	out, err := runGit(cmdCtx, dir, args...)
	if err != nil {
		return nil, fmt.Errorf("git log %s: %w (%s)", relPath, err, out)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	var commits []playbookCommit
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		commits = append(commits, playbookCommit{
			Sha:     parts[0],
			Date:    parts[1],
			Summary: parts[2],
			Source:  source,
		})
	}
	return commits, nil
}

// gitShowAtCommit returns the body of relPath at sha (in dir's repo).
// Returns ("", nil) when the commit doesn't touch this path. The
// resulting bytes are exactly what was committed — no normalisation.
func gitShowAtCommit(ctx context.Context, dir, sha, relPath string) (string, error) {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return "", fmt.Errorf("no git repo at %s", dir)
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := runGit(cmdCtx, dir, "show", sha+":"+relPath)
	if err != nil {
		return "", fmt.Errorf("git show %s:%s: %w (%s)", sha, relPath, err, out)
	}
	return out, nil
}

// commitPlaybookChange stages relPath (relative to the user dir) and
// creates a commit if there's anything staged. No-op when the file
// is byte-identical to HEAD. message is the commit subject; the
// caller picks an auto-generated default when the operator didn't
// supply one.
func commitPlaybookChange(ctx context.Context, dir, relPath, message string) error {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		// No repo (init failed earlier) — write succeeded but we
		// can't record history. Return cleanly so save still
		// reports success.
		return nil
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if out, err := runGit(cmdCtx, dir, "add", relPath); err != nil {
		return fmt.Errorf("git add %s: %w (%s)", relPath, err, out)
	}
	staged, err := gitHasStagedChanges(cmdCtx, dir)
	if err != nil {
		return err
	}
	if !staged {
		return nil // no-op save: file matches HEAD
	}
	if out, err := runGit(cmdCtx, dir, "commit", "-m", message); err != nil {
		return fmt.Errorf("git commit: %w (%s)", err, out)
	}
	return nil
}
