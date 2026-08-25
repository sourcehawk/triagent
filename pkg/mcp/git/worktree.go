package git

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// newWorktreeBranch returns "triagent-proposal/<issueNum>-<8-hex>" — uniqueness
// is for parallel draft_pr invocations against the same issue (rare but
// possible when an edit-mode iteration races a fresh request).
func newWorktreeBranch(issueNum int) string {
	var buf [4]byte
	_, _ = rand.Read(buf[:])
	return fmt.Sprintf("triagent-proposal/%d-%s", issueNum, hex.EncodeToString(buf[:]))
}

// worktreeRoot returns the parent directory under which per-repo
// worktrees are nested. Tests override via Server.worktreeRootOverride
// to keep their bookkeeping inside t.TempDir() — the production
// default lives at $XDG_RUNTIME_DIR/triagent-mcp-git/codefix (tmpfs on
// most distros), falling back to os.TempDir().
func (s *Server) worktreeRoot() string {
	if s.worktreeRootOverride != "" {
		return s.worktreeRootOverride
	}
	if x := os.Getenv("XDG_RUNTIME_DIR"); x != "" {
		return filepath.Join(x, "triagent-mcp-git", "codefix")
	}
	return filepath.Join(os.TempDir(), "triagent-mcp-git", "codefix")
}

// createWorktree adds a new branch worktree off baseRef, into a temp
// dir under worktreeRoot. Caller is responsible for removeWorktree on
// exit. baseRef defaults to "main" when empty.
func (s *Server) createWorktree(ctx context.Context, repoDir, branch, baseRef string) (string, error) {
	if baseRef == "" {
		baseRef = "main"
	}
	if err := os.MkdirAll(s.worktreeRoot(), 0o700); err != nil {
		return "", fmt.Errorf("mkdir worktree root: %w", err)
	}
	sanitised := branchToDirSegment(branch)
	wt := filepath.Join(s.worktreeRoot(), fmt.Sprintf("%s-%s-%s", s.owner, s.name, sanitised))
	if err := runGit(ctx, repoDir, "worktree", "add", "-b", branch, wt, baseRef); err != nil {
		return "", fmt.Errorf("git worktree add %s -> %s: %w", baseRef, wt, err)
	}
	return wt, nil
}

// createDetachedWorktree checks out ref as a detached HEAD in a fresh
// dir under worktreeRoot, for read-only sub-agents whose Read/Glob/Grep
// must see the tree at exactly that ref (the cache clone's own checkout
// is never fast-forwarded, so it is the wrong place to run them). The
// dir name carries a random suffix so concurrent calls for the same
// repo don't collide. Caller is responsible for removeWorktree on exit.
func (s *Server) createDetachedWorktree(ctx context.Context, repoDir, ref string) (string, error) {
	if err := os.MkdirAll(s.worktreeRoot(), 0o700); err != nil {
		return "", fmt.Errorf("mkdir worktree root: %w", err)
	}
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("worktree suffix: %w", err)
	}
	wt := filepath.Join(s.worktreeRoot(), fmt.Sprintf("%s-%s-research-%x", s.owner, s.name, suffix))
	if err := runGit(ctx, repoDir, "worktree", "add", "--detach", wt, ref); err != nil {
		return "", fmt.Errorf("git worktree add --detach %s -> %s: %w", ref, wt, err)
	}
	return wt, nil
}

// removeWorktree tears down the worktree directory and prunes the
// corresponding bookkeeping in the bare clone. The branch ref on the
// remote (post-push) is unaffected.
func (s *Server) removeWorktree(ctx context.Context, repoDir, wt string) error {
	if err := runGit(ctx, repoDir, "worktree", "remove", "--force", wt); err != nil {
		// Even if git refuses (e.g. dir already gone), try to clean
		// the orphan dir and prune.
		_ = os.RemoveAll(wt)
	}
	return runGit(ctx, repoDir, "worktree", "prune")
}

// sweepStaleWorktrees removes any worktree dir under worktreeRoot for
// THIS server's repo (prefix "<owner>-<name>-") whose mtime is older
// than cutoff. Called at server startup so a previous-process crash
// doesn't leak indefinitely. Other repos' worktree dirs are left
// untouched; each triagent-mcp git process sweeps its own slice.
func (s *Server) sweepStaleWorktrees(ctx context.Context, repoDir string, cutoff time.Duration) error {
	root := s.worktreeRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	prefix := s.owner + "-" + s.name + "-"
	threshold := time.Now().Add(-cutoff)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		full := filepath.Join(root, e.Name())
		info, err := os.Stat(full)
		if err != nil || info.ModTime().After(threshold) {
			continue
		}
		_ = runGit(ctx, repoDir, "worktree", "remove", "--force", full)
		_ = os.RemoveAll(full)
	}
	_ = runGit(ctx, repoDir, "worktree", "prune")
	return nil
}

// branchToDirSegment rewrites slashes/colons in a branch name to single
// hyphens so the dirname stays one filesystem segment.
func branchToDirSegment(branch string) string {
	out := make([]byte, 0, len(branch))
	for i := 0; i < len(branch); i++ {
		c := branch[i]
		if c == '/' || c == ':' {
			out = append(out, '-')
			continue
		}
		out = append(out, c)
	}
	return string(out)
}
