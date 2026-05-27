package wiki

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWikiResolveEntities_ExactAndNear(t *testing.T) {
	t.Parallel()
	srv := &Server{vaultPath: seedVault(t)}
	_, out, err := srv.wikiResolveEntities(context.Background(), nil, resolveEntitiesIn{
		Services: []string{"zeebe-broker", "Zeebe Broker"},
	})
	require.NoError(t, err, "wikiResolveEntities")
	require.Len(t, out.Resolution, 2, "expected one resolution per input")

	assert.True(t, out.Resolution[0].Exact, "first input is canonical → exact match")
	assert.Equal(t, "zeebe-broker", out.Resolution[0].Input)

	assert.False(t, out.Resolution[1].Exact, "second input is fuzzy → not exact")
	assert.Equal(t, "Zeebe Broker", out.Resolution[1].Input, "input is echoed verbatim")
	require.NotEmpty(t, out.Resolution[1].Near, "fuzzy input should have a near-match")
	assert.Equal(t, "zeebe-broker", out.Resolution[1].Near[0], "near should surface the canonical name")
}

func TestWikiResolveEntities_EmptyInput(t *testing.T) {
	t.Parallel()
	srv := &Server{vaultPath: seedVault(t)}
	res, out, err := srv.wikiResolveEntities(context.Background(), nil, resolveEntitiesIn{})
	require.NoError(t, err)
	assert.Nil(t, res, "no MCP-level error")
	assert.NotNil(t, out.Resolution, "must be [] not null")
	assert.Empty(t, out.Resolution)
}

func TestWikiResolveEntities_AcceptsFuzzyInputWithoutValidating(t *testing.T) {
	t.Parallel()
	// The whole point of this tool is to take fuzzy input — unlike
	// wiki_correlate / wiki_search, it doesn't reject "Bad Name" with
	// a validation error.
	srv := &Server{vaultPath: seedVault(t)}
	res, out, err := srv.wikiResolveEntities(context.Background(), nil, resolveEntitiesIn{
		Services: []string{"Zeebe Broker"}, // spaces + capitals
	})
	require.NoError(t, err)
	assert.Nil(t, res, "fuzzy input must NOT produce an MCP error")
	require.Len(t, out.Resolution, 1)
	assert.Equal(t, "Zeebe Broker", out.Resolution[0].Input)
}
