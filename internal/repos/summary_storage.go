package repos

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// SummaryFrontmatter is the YAML frontmatter of an architecture-summary cache
// file. Kept in sync with mcp/internal/git.SummaryFrontmatter — both modules
// read/write the same on-disk layout.
type SummaryFrontmatter struct {
	GeneratedAt time.Time `yaml:"generated_at"`
	Kind        string    `yaml:"kind"`
	Focus       string    `yaml:"focus,omitempty"`
	Model       string    `yaml:"model,omitempty"`
	ByteCount   int       `yaml:"byte_count,omitempty"`
	// Error is non-empty when generation failed (e.g. sub-agent timed out).
	Error string `yaml:"error,omitempty"`
}

// SummaryFile pairs the parsed frontmatter with the markdown body.
type SummaryFile struct {
	Frontmatter SummaryFrontmatter
	Body        string
}

// ErrSummaryNotFound is returned by ReadSummary when the cache file is absent.
var ErrSummaryNotFound = errors.New("architecture summary not cached")

// SummaryPath returns the canonical cache-file path for a repo's
// architecture summary. Summaries sit under a `summaries/` subroot of
// the cache dir — separate from the clone trees at `<cacheDir>/<owner>/<name>`
// — so our metadata files don't block triagent-mcp's EnsureClone (which
// refuses to clone into a non-empty directory) and don't surface as
// untracked files inside the cloned repo's working tree. The layout
// matches mcp/internal/git.SummaryPath — both modules must agree.
func SummaryPath(cacheDir, owner, name string) string {
	return filepath.Join(cacheDir, "summaries", owner, name, "architecture_summary.md")
}

// DefaultGitCacheDir returns the cache root the triagent-git MCP server falls
// back to when --cache-dir is empty: $XDG_CACHE_HOME/triagent-mcp/git, or
// ~/.cache/triagent-mcp/git when XDG_CACHE_HOME is unset. Mirrors
// mcp/internal/git.defaultCacheDir so the launcher and the MCP server
// always agree on where cache files live — the launcher must call this
// when its own GitCacheDir option is empty, otherwise relative paths
// resolve under the launcher's working directory and silently diverge
// from where triagent-mcp itself writes.
func DefaultGitCacheDir() (string, error) {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "triagent-mcp", "git"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".cache", "triagent-mcp", "git"), nil
}

// BaselinePath returns the path to the last raw AI-generated body for a
// repo, kept alongside the active cache file as a frozen reference. The
// active cache file (SummaryPath) may diverge from the baseline if an
// operator hand-edited it; the diff between the two is the operator's
// contribution. Each successful AI generation overwrites both files,
// resetting the baseline; manual edits update only the active file.
//
// Plain markdown — no frontmatter — because nothing reads it except the
// diff machinery, and a frontmatter block would just be noise in the
// resulting unified diff. Same `summaries/` subroot as SummaryPath so
// both files move together if the layout ever changes.
func BaselinePath(cacheDir, owner, name string) string {
	return filepath.Join(cacheDir, "summaries", owner, name, "architecture_summary.baseline.md")
}

// ErrBaselineNotFound is returned by ReadBaselineBody when no baseline
// has been written yet (e.g. the active file was hand-authored without
// a prior AI generation).
var ErrBaselineNotFound = errors.New("architecture summary baseline not cached")

// ReadBaselineBody returns the raw markdown body of the last AI-generated
// summary for a repo. Returns ErrBaselineNotFound when no baseline
// exists yet — callers treat that as "no operator edits to track."
func ReadBaselineBody(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrBaselineNotFound
		}
		return "", fmt.Errorf("read baseline file %s: %w", path, err)
	}
	return string(data), nil
}

// WriteBaselineBody atomically writes the raw markdown body. Called by
// the generator on every successful AI run so the next regen has a
// reference point for diffing operator edits.
func WriteBaselineBody(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// OperatorEditsDiff returns a unified diff (default 3-line context)
// between the AI-generated baseline and the active body the operator
// has edited. Empty diff + hasEdits=false means the active matches the
// baseline (no edits) or the baseline doesn't exist yet (nothing to
// diff against). Shells out to `git diff --no-index` rather than
// pulling in a Go diff library — git is already a hard dep of the
// triagent-git plugin and produces the canonical unified-diff format.
func OperatorEditsDiff(ctx context.Context, baselinePath, activeBody string) (diff string, hasEdits bool, err error) {
	baseline, err := ReadBaselineBody(baselinePath)
	if errors.Is(err, ErrBaselineNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if baseline == activeBody {
		return "", false, nil
	}
	// Write the active body to a temp file alongside the baseline so
	// `git diff --no-index` can compare two real paths. Using a temp
	// file (vs piping via stdin) is the simplest cross-platform path.
	tmp, err := os.CreateTemp("", "c1-active-*.md")
	if err != nil {
		return "", false, fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.WriteString(activeBody); err != nil {
		_ = tmp.Close()
		return "", false, fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", false, fmt.Errorf("close temp: %w", err)
	}
	// `git diff --no-index` exits 0 when files match, 1 when they
	// differ (still a "success" for our purposes), 2+ for real errors.
	// We've already short-circuited on equality, so we expect exit 1.
	cmd := exec.CommandContext(ctx, "git", "diff", "--no-index", "--unified=3",
		baselinePath, tmpPath)
	out, runErr := cmd.Output()
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) && ee.ExitCode() == 1 {
			// expected — diff was non-empty
		} else {
			return "", false, fmt.Errorf("git diff: %w", runErr)
		}
	}
	return string(out), true, nil
}

// ReadSummary parses the cache file at path. Returns ErrSummaryNotFound when
// the file is absent.
func ReadSummary(path string) (*SummaryFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrSummaryNotFound
		}
		return nil, fmt.Errorf("read summary file %s: %w", path, err)
	}
	return parseSummaryFile(data)
}

// WriteSummary atomically writes the cache file at path. Creates the parent
// directory when missing. Atomic via tmp+rename so a crash mid-write doesn't
// leave a partial file.
//
// Kept in sync with mcp/internal/git.WriteSummary — both modules write
// frontmatter + body in the same shape so what triagent-mcp produces the
// launcher can read.
func WriteSummary(path string, sum *SummaryFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	body, err := serializeSummaryFile(sum)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func parseSummaryFile(data []byte) (*SummaryFile, error) {
	s := string(data)
	if !strings.HasPrefix(s, "---\n") {
		return nil, errors.New("summary file: missing frontmatter delimiter at start")
	}
	rest := s[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return nil, errors.New("summary file: missing frontmatter closing delimiter")
	}
	fmYAML := rest[:end]
	body := rest[end+len("\n---\n"):]
	var fm SummaryFrontmatter
	if err := yaml.Unmarshal([]byte(fmYAML), &fm); err != nil {
		return nil, fmt.Errorf("summary file: parse frontmatter: %w", err)
	}
	return &SummaryFile{Frontmatter: fm, Body: body}, nil
}

func serializeSummaryFile(sum *SummaryFile) ([]byte, error) {
	fmYAML, err := yaml.Marshal(sum.Frontmatter)
	if err != nil {
		return nil, fmt.Errorf("summary file: marshal frontmatter: %w", err)
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fmYAML)
	b.WriteString("---\n")
	b.WriteString(sum.Body)
	return []byte(b.String()), nil
}
