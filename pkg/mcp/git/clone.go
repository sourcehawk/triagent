package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// EnsureClone returns the path to the cached clone of the configured repo.
// Clones on cache miss; fetches on hit. v1 assumes SSH access — clone URL
// is `git@github.com:<owner>/<name>.git` and inherits the operator's SSH
// agent / `~/.ssh/config`.
//
// Concurrent calls for the same repo serialize on a per-Server mutex so
// the first call wins the clone and subsequent calls pick up the populated
// cache.
func (s *Server) EnsureClone(ctx context.Context) (string, error) {
	s.cloneOnce.Lock()
	defer s.cloneOnce.Unlock()

	dir := filepath.Join(s.cacheDir, s.owner, s.name)
	gitDir := filepath.Join(dir, ".git")

	if _, err := os.Stat(gitDir); err == nil {
		// Already cloned — fetch latest refs.
		if err := runGit(ctx, dir, "fetch", "--prune", "--tags"); err != nil {
			return "", fmt.Errorf("git fetch %s/%s: %w", s.owner, s.name, err)
		}
		return dir, nil
	}

	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return "", fmt.Errorf("mkdir cache: %w", err)
	}
	url := fmt.Sprintf("git@github.com:%s/%s.git", s.owner, s.name)
	// Blobless filter avoids pulling the full file contents up front; git
	// fetches blobs on demand when sub-agents read files via Read/Grep.
	if err := runGit(ctx, "", "clone", "--filter=blob:none", url, dir); err != nil {
		return "", fmt.Errorf("git clone %s: %w", url, err)
	}
	return dir, nil
}

// runGit runs a git command in cwd (empty cwd = inherit). Stderr is
// captured and surfaced in the error so the operator sees auth/network
// failures verbatim.
func runGit(ctx context.Context, cwd string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w (output: %s)", "git "+joinArgs(args), err, trimTo(string(out), 800))
	}
	return nil
}

// gitOutput runs git and returns its stdout, separating stderr for error
// context. Used by discovery tools that need to parse the output.
func gitOutput(ctx context.Context, cwd string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	stdout, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		return "", fmt.Errorf("%s: %w (stderr: %s)", "git "+joinArgs(args), err, trimTo(stderr, 400))
	}
	return string(stdout), nil
}

func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}

func trimTo(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// defaultCacheDir resolves the default cache root: $XDG_CACHE_HOME/triagent-mcp/git,
// falling back to ~/.cache/triagent-mcp/git.
func defaultCacheDir() (string, error) {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "triagent-mcp", "git"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "triagent-mcp", "git"), nil
}

