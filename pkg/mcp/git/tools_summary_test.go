package git

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRepoArchitectureSummary_Exists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	when := time.Date(2026, 5, 8, 14, 30, 0, 0, time.UTC)
	require.NoError(t, WriteSummary(SummaryPath(dir, "example-org", "operate"), &SummaryFile{
		Frontmatter: SummaryFrontmatter{
			GeneratedAt: when,
			Kind:        "freeform",
			ByteCount:   42,
		},
		Body: "# example-org/operate\n\n…\n",
	}))

	// Field names match the existing Server struct: cacheDir / owner / name.
	s := &Server{cacheDir: dir, owner: "example-org", name: "operate"}
	_, out, err := s.getRepoArchitectureSummary(context.Background(), nil, getArchitectureSummaryIn{})
	require.NoError(t, err)
	assert.True(t, out.Exists)
	assert.Equal(t, when, out.GeneratedAt)
	assert.Equal(t, "freeform", out.Kind)
	assert.Contains(t, out.Content, "# example-org/operate")
	assert.Empty(t, out.Hint)
	assert.Empty(t, out.Error, "success-path output must not carry an Error string")
}

func TestGetRepoArchitectureSummary_Missing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	s := &Server{cacheDir: dir, owner: "example-org", name: "operate"}
	_, out, err := s.getRepoArchitectureSummary(context.Background(), nil, getArchitectureSummaryIn{})
	require.NoError(t, err)
	assert.False(t, out.Exists)
	assert.NotEmpty(t, out.Hint, "missing-cache response must carry a hint")
	assert.Contains(t, out.Hint, "Linked repositories",
		"hint must point the agent at the LinkedRepos description fallback")
}

func TestGetRepoArchitectureSummary_ErrorVariantSurfaces(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	when := time.Now().UTC()
	require.NoError(t, WriteSummary(SummaryPath(dir, "x", "y"), &SummaryFile{
		Frontmatter: SummaryFrontmatter{
			GeneratedAt: when,
			Kind:        "freeform",
			Error:       "sub-agent timeout after 15m",
		},
		Body: "> Generation failed: sub-agent timeout after 15m\n",
	}))

	s := &Server{cacheDir: dir, owner: "x", name: "y"}
	_, out, err := s.getRepoArchitectureSummary(context.Background(), nil, getArchitectureSummaryIn{})
	require.NoError(t, err)
	assert.True(t, out.Exists, "error-variant cache file is still 'exists' — body carries the failure note")
	assert.Equal(t, "sub-agent timeout after 15m", out.Error,
		"structured Error field must mirror the cache file's frontmatter")
	assert.Contains(t, out.Content, "Generation failed",
		"Content body still carries the human-readable failure note")
}
