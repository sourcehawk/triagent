package git

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummaryPath(t *testing.T) {
	t.Parallel()
	got := SummaryPath("/cache", "example-org", "operate")
	want := filepath.Join("/cache", "summaries", "example-org", "operate", "architecture_summary.md")
	assert.Equal(t, want, got,
		"summary lives under summaries/ — keeps it out of the clone working tree at <cacheDir>/<owner>/<name>")
}

func TestSummary_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := SummaryPath(dir, "example-org", "operate")
	when := time.Date(2026, 5, 8, 14, 30, 0, 0, time.UTC)
	in := &SummaryFile{
		Frontmatter: SummaryFrontmatter{
			GeneratedAt: when,
			Kind:        "freeform",
			Model:       "claude-opus-4-7",
			ByteCount:   42,
		},
		Body: "# example-org/operate — architecture summary\n\nHello.\n",
	}
	require.NoError(t, WriteSummary(path, in))

	got, err := ReadSummary(path)
	require.NoError(t, err)
	assert.Equal(t, when, got.Frontmatter.GeneratedAt)
	assert.Equal(t, "freeform", got.Frontmatter.Kind)
	assert.Equal(t, "claude-opus-4-7", got.Frontmatter.Model)
	assert.Equal(t, 42, got.Frontmatter.ByteCount)
	assert.Equal(t, in.Body, got.Body)
}

func TestSummary_ReadNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := ReadSummary(SummaryPath(dir, "missing", "repo"))
	assert.ErrorIs(t, err, ErrSummaryNotFound)
}

func TestSummary_ErrorVariant(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := SummaryPath(dir, "x", "y")
	in := &SummaryFile{
		Frontmatter: SummaryFrontmatter{
			GeneratedAt: time.Now().UTC().Truncate(time.Second),
			Kind:        "freeform",
			Error:       "sub-agent timeout after 15m",
		},
		Body: "> Generation failed: sub-agent timeout after 15m\n",
	}
	require.NoError(t, WriteSummary(path, in))
	got, err := ReadSummary(path)
	require.NoError(t, err)
	assert.Equal(t, "sub-agent timeout after 15m", got.Frontmatter.Error)
}

func TestSummary_ParseMissingFrontmatter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := SummaryPath(dir, "x", "y")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("no frontmatter here\n"), 0o600))
	_, err := ReadSummary(path)
	assert.Error(t, err)
}

// BaselinePath + WriteBaselineBody parity with the launcher-side
// `repos` package: both modules must agree on the on-disk layout for
// baseline tracking to round-trip correctly between the AI generator
// (writes baseline) and the launcher's diff helper (reads it).

func TestBaselinePath_LayoutMatchesActiveSibling(t *testing.T) {
	t.Parallel()
	active := SummaryPath("/cache", "example-org", "operate")
	baseline := BaselinePath("/cache", "example-org", "operate")
	assert.Equal(t, filepath.Dir(active), filepath.Dir(baseline),
		"baseline must live next to the active cache file")
	assert.NotEqual(t, active, baseline)
}

func TestWriteBaselineBody_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := BaselinePath(dir, "example-org", "operate")
	body := "# example-org/operate — architecture summary\n\n## Top-level structure\n\nfoo.\n"
	require.NoError(t, WriteBaselineBody(path, body))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, body, string(got),
		"baseline file is plain markdown — no frontmatter or other framing")
}
