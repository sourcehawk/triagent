package wiki

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWikiCorrelate_RanksByOverlap(t *testing.T) {
	t.Parallel()
	srv := &Server{vaultPath: seedVault(t)}
	_, out, err := srv.wikiCorrelate(context.Background(), nil, wikiCorrelateIn{
		Services: []string{"zeebe-broker"},
		Errors:   []string{"oom-kill"},
	})
	require.NoError(t, err, "wikiCorrelate")
	require.Len(t, out.Correlations, 1, "expected 1 correlation (INC-1 has both entities)")
	got := out.Correlations[0]
	assert.Equal(t, "inc-1-broker-ooms", got.Slug)
	assert.Equal(t, 2, got.Score, "expected score=2 (1 service + 1 error overlap)")
}

func TestWikiCorrelate_EmptyQueryReturnsEmpty(t *testing.T) {
	t.Parallel()
	srv := &Server{vaultPath: seedVault(t)}
	_, out, err := srv.wikiCorrelate(context.Background(), nil, wikiCorrelateIn{})
	require.NoError(t, err, "wikiCorrelate")
	assert.Empty(t, out.Correlations, "expected empty result on empty query")
}

func TestWikiCorrelate_RespectsLimit(t *testing.T) {
	t.Parallel()
	srv := &Server{vaultPath: seedVault(t)}
	_, out, err := srv.wikiCorrelate(context.Background(), nil, wikiCorrelateIn{
		Symptoms: []string{"shard-stuck", "stuck-reconciliation"},
		Limit:    1,
	})
	require.NoError(t, err, "wikiCorrelate")
	require.Len(t, out.Correlations, 1, "expected exactly 1 result with limit=1")
}

func TestWikiCorrelate_MalformedNameReturnsError(t *testing.T) {
	t.Parallel()
	srv := &Server{vaultPath: seedVault(t)}
	res, _, err := srv.wikiCorrelate(context.Background(), nil, wikiCorrelateIn{
		Services: []string{"Zeebe Broker"},
	})
	require.NoError(t, err, "wikiCorrelate")
	require.NotNil(t, res, "expected IsError result for malformed name")
	require.True(t, res.IsError, "expected IsError result for malformed name")
}

func TestWikiCorrelate_SurfacesNearMatchInResolution(t *testing.T) {
	t.Parallel()
	srv := &Server{vaultPath: seedVault(t)}
	// "broker" is well-formed but no exact match — wiki_correlate
	// finds nothing, but resolution should suggest "zeebe-broker".
	_, out, err := srv.wikiCorrelate(context.Background(), nil, wikiCorrelateIn{
		Services: []string{"broker"},
	})
	require.NoError(t, err, "wikiCorrelate")
	require.Len(t, out.Resolution, 1, "expected 1 resolution entry")
	r := out.Resolution[0]
	assert.False(t, r.Exact, "expected exact=false")
	require.NotEmpty(t, r.Near, "expected near=[zeebe-broker, ...]")
	assert.Equal(t, "zeebe-broker", r.Near[0], "expected near=[zeebe-broker, ...]")
}

func TestWikiCorrelate_ResolutionMarksExact(t *testing.T) {
	t.Parallel()
	srv := &Server{vaultPath: seedVault(t)}
	_, out, err := srv.wikiCorrelate(context.Background(), nil, wikiCorrelateIn{
		Services: []string{"zeebe-broker"},
	})
	require.NoError(t, err, "wikiCorrelate")
	require.Len(t, out.Resolution, 1, "expected exact=true for canonical name")
	assert.True(t, out.Resolution[0].Exact, "expected exact=true for canonical name")
}
