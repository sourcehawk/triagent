package git

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// SummaryFrontmatter is the YAML frontmatter of an architecture-summary cache file.
type SummaryFrontmatter struct {
	GeneratedAt time.Time `yaml:"generated_at"`
	Kind        string    `yaml:"kind"`
	Focus       string    `yaml:"focus,omitempty"`
	Model       string    `yaml:"model,omitempty"`
	ByteCount   int       `yaml:"byte_count,omitempty"`
	// Error is non-empty when generation failed (e.g. sub-agent timed out).
	// The Body in that case is a single one-line "> Generation failed: …" hint.
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
// architecture summary. Summaries live OUTSIDE the clone working tree
// — at `<cacheDir>/summaries/<owner>/<name>/architecture_summary.md`,
// while clones live at `<cacheDir>/<owner>/<name>` — because writing
// our cache files into the clone dir blocks subsequent
// EnsureClone calls (`git clone` refuses to write into a non-empty
// directory). Keeping the trees separate also keeps our metadata out
// of the repo's working tree where it would surface as untracked
// files to anyone running `git status` against the cache.
func SummaryPath(cacheDir, owner, name string) string {
	return filepath.Join(cacheDir, "summaries", owner, name, "architecture_summary.md")
}

// BaselinePath returns the path to the last raw AI-generated body.
// Sibling to SummaryPath; the generator writes both on every successful
// run so the launcher has a reference point for diffing operator edits
// on the next regen. The active file (SummaryPath) may diverge if an
// operator hand-edits; the baseline stays frozen until the next AI run.
//
// Plain markdown — no frontmatter — because nothing reads it except the
// diff machinery, and a frontmatter block would be noise in the diff.
// Same `summaries/` subroot as SummaryPath so both files move together
// if the layout ever changes.
func BaselinePath(cacheDir, owner, name string) string {
	return filepath.Join(cacheDir, "summaries", owner, name, "architecture_summary.baseline.md")
}

// WriteBaselineBody atomically writes the raw markdown body to the
// baseline path. Called by the generator on every successful AI run.
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

// ReadSummary parses the cache file at path. Returns ErrSummaryNotFound when
// the file is absent (caller distinguishes "no summary yet" from real errors).
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
