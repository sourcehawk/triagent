package git

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/sourcehawk/triagent/pkg/mcp/subagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubRunner returns a fixed sub-agent result without spawning Claude.
type stubRunner struct {
	body string
	err  error
}

func (s stubRunner) Run(ctx context.Context, opts subagent.Options) (subagent.Result, error) {
	if s.err != nil {
		return subagent.Result{}, s.err
	}
	return subagent.Result{Summary: s.body}, nil
}

func TestGenerateArchitectureSummary_Success(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Simulate a realistic sub-agent response: a few lines of preamble,
	// the sentinel-wrapped body, then trailing commentary. The generator
	// must cache only the fenced body.
	wantBody := "# example-org/operate — architecture summary\n\n## Top-level structure\n…"
	rawResponse := "I have enough context now.\n\n" +
		"<<<BEGIN_SUMMARY\n" +
		wantBody + "\n" +
		"SUMMARY>>>\n\n" +
		"Let me know if you want me to drill in further."
	when := time.Date(2026, 5, 8, 14, 30, 0, 0, time.UTC)

	res, err := GenerateArchitectureSummary(context.Background(), GenerateOptions{
		GitCacheDir: dir,
		RepoPath:    filepath.Join(dir, "example-org", "operate"),
		Owner:       "example-org",
		Name:        "operate",
		Kind:        "freeform",
		Model:       "claude-opus-4-7",
		Now:         func() time.Time { return when },
		Runner:      stubRunner{body: rawResponse},
	})
	require.NoError(t, err)
	assert.Equal(t, when, res.GeneratedAt)
	assert.False(t, res.TimedOut)

	// File on disk has the extracted body (no preamble, no sentinels, no
	// trailing commentary) and the frontmatter we expect.
	got, err := ReadSummary(SummaryPath(dir, "example-org", "operate"))
	require.NoError(t, err)
	assert.Equal(t, wantBody, got.Body,
		"cached body must be the fenced content only — preamble and trailing commentary must not leak through")
	assert.Equal(t, "freeform", got.Frontmatter.Kind)
	assert.Equal(t, "claude-opus-4-7", got.Frontmatter.Model)
	assert.Equal(t, len(wantBody), got.Frontmatter.ByteCount)
	assert.Empty(t, got.Frontmatter.Error)
}

func TestGenerateArchitectureSummary_TimeoutWritesErrorVariant(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	when := time.Date(2026, 5, 8, 14, 30, 0, 0, time.UTC)

	res, err := GenerateArchitectureSummary(context.Background(), GenerateOptions{
		GitCacheDir: dir,
		RepoPath:    filepath.Join(dir, "x", "y"),
		Owner:       "x",
		Name:        "y",
		Kind:        "freeform",
		Now:         func() time.Time { return when },
		Runner:      stubRunner{err: context.DeadlineExceeded},
	})
	require.NoError(t, err, "generator returns nil even on sub-agent failure (cache file captures the error)")
	assert.True(t, res.TimedOut)

	got, err := ReadSummary(SummaryPath(dir, "x", "y"))
	require.NoError(t, err)
	assert.Contains(t, got.Frontmatter.Error, "timeout")
	assert.Contains(t, got.Body, "Generation failed")
}

func TestGenerateArchitectureSummary_NonTimeoutErrorWritesErrorVariant(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	when := time.Date(2026, 5, 8, 14, 30, 0, 0, time.UTC)

	res, err := GenerateArchitectureSummary(context.Background(), GenerateOptions{
		GitCacheDir: dir,
		RepoPath:    filepath.Join(dir, "x", "y"),
		Owner:       "x",
		Name:        "y",
		Kind:        "freeform",
		Now:         func() time.Time { return when },
		Runner:      stubRunner{err: errors.New("clone failed")},
	})
	require.NoError(t, err)
	assert.False(t, res.TimedOut)

	got, err := ReadSummary(SummaryPath(dir, "x", "y"))
	require.NoError(t, err)
	assert.Contains(t, got.Frontmatter.Error, "clone failed")
}

// On regen failure, an existing real cache file (AI baseline OR
// operator hand-edit — anything without an Error frontmatter) must be
// preserved. Operators retry refreshes routinely; the SSE phase=error
// event surfaces the failure separately. Clobbering with an error
// variant would silently destroy hand-edits and force a re-author.
func TestGenerateArchitectureSummary_PreservesExistingOnFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	when := time.Date(2026, 5, 8, 14, 30, 0, 0, time.UTC)

	// Seed an "operator hand-edit" file at the cache path — real
	// frontmatter, real body, no Error field.
	priorBody := "# x/y — architecture summary\n\n## Top-level structure\n\noperator-authored content.\n"
	require.NoError(t, WriteSummary(SummaryPath(dir, "x", "y"), &SummaryFile{
		Frontmatter: SummaryFrontmatter{
			GeneratedAt: when.Add(-1 * time.Hour),
			Kind:        "freeform",
			Model:       "operator-edit",
			ByteCount:   len(priorBody),
		},
		Body: priorBody,
	}))

	// Now run a refresh that fails.
	res, err := GenerateArchitectureSummary(context.Background(), GenerateOptions{
		GitCacheDir: dir,
		RepoPath:    filepath.Join(dir, "x", "y"),
		Owner:       "x",
		Name:        "y",
		Kind:        "freeform",
		Now:         func() time.Time { return when },
		Runner:      stubRunner{err: errors.New("sub-agent crashed")},
	})
	require.NoError(t, err)
	assert.False(t, res.TimedOut)

	// The cache file must still hold the operator's content; no error
	// variant was written.
	got, err := ReadSummary(SummaryPath(dir, "x", "y"))
	require.NoError(t, err)
	assert.Equal(t, priorBody, got.Body, "operator's hand-edit body must survive a failed regen")
	assert.Empty(t, got.Frontmatter.Error,
		"failure must not stamp Error on a preserved cache file")
	assert.Equal(t, "operator-edit", got.Frontmatter.Model,
		"preserved frontmatter must be unchanged")
}

// When the existing cache file is itself an error variant (left over
// from a prior failed run), the new failure should overwrite — the
// new error message may differ and there's nothing worth preserving.
func TestGenerateArchitectureSummary_OverwritesExistingErrorVariantOnFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	when := time.Date(2026, 5, 8, 14, 30, 0, 0, time.UTC)

	// Seed a prior error-variant file.
	require.NoError(t, WriteSummary(SummaryPath(dir, "x", "y"), &SummaryFile{
		Frontmatter: SummaryFrontmatter{
			GeneratedAt: when.Add(-1 * time.Hour),
			Kind:        "freeform",
			Error:       "prior failure: timeout",
		},
		Body: "> Generation failed: prior failure: timeout\n",
	}))

	_, err := GenerateArchitectureSummary(context.Background(), GenerateOptions{
		GitCacheDir: dir,
		RepoPath:    filepath.Join(dir, "x", "y"),
		Owner:       "x",
		Name:        "y",
		Kind:        "freeform",
		Now:         func() time.Time { return when },
		Runner:      stubRunner{err: errors.New("new failure: clone refused")},
	})
	require.NoError(t, err)

	got, err := ReadSummary(SummaryPath(dir, "x", "y"))
	require.NoError(t, err)
	assert.Contains(t, got.Frontmatter.Error, "new failure: clone refused",
		"new error message must replace the prior error variant")
}
