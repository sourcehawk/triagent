package wiki

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWikiSchema_ReturnsAuthoringMarkdown(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, nil)
	_, out, err := srv.wikiSchema(context.Background(), nil, wikiSchemaIn{})
	require.NoError(t, err)
	require.NotEmpty(t, out.Schema, "expected non-empty schema markdown")
	for _, want := range []string{
		"schema_version: 1",
		"links",
		"lowercase-with-hyphens",
		"## Summary",
		"## Root cause",
		"## Fix",
	} {
		assert.True(t, strings.Contains(out.Schema, want), "schema markdown missing %q", want)
	}
}

func TestWikiSchema_NamesProseStyle(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, nil)
	_, out, err := srv.wikiSchema(context.Background(), nil, wikiSchemaIn{})
	require.NoError(t, err)
	assert.Contains(t, out.Schema, "## Prose style")
}
