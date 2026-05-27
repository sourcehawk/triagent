package wiki

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedVault(t *testing.T) string {
	t.Helper()
	vault := t.TempDir()
	mustWrite(t, filepath.Join(vault, "entries/inc-1-broker-ooms.md"),
		`---
id: inc-1-broker-ooms
date: 2026-04-12
title: Broker OOMs during reconciliation
status: resolved
severity: sev1
services: [zeebe-broker]
errors: [oom-kill]
symptoms: [stuck-reconciliation]
sources:
  investigation_session: sess-1
---

## Summary
The broker hit OOM under reconciliation pressure.

## Root cause
[[zeebe-broker]] heap exhausted by [[oom-kill]].

## Fix
Bumped heap.
`)
	mustWrite(t, filepath.Join(vault, "entries/inc-2-es-slow.md"),
		`---
id: inc-2-es-slow
date: 2026-04-13
title: Elasticsearch shard rebalancing slow
status: open
severity: sev2
services: [elasticsearch]
errors: []
symptoms: [shard-stuck]
sources:
  investigation_session: sess-2
---

## Summary
ES shards stuck on rebalance.

## Root cause
TBD.

## Fix
TBD.
`)
	mustWrite(t, filepath.Join(vault, "entities/services/zeebe-broker.md"), "stub")
	return vault
}

func TestWikiSearch_FilterByService(t *testing.T) {
	t.Parallel()
	srv := &Server{vaultPath: seedVault(t)}
	_, out, err := srv.wikiSearch(context.Background(), nil, wikiSearchIn{
		Filters: wikiSearchFilters{Services: []string{"zeebe-broker"}},
	})
	require.NoError(t, err, "wikiSearch")
	require.Len(t, out.Hits, 1, "expected 1 hit")
	assert.Equal(t, "inc-1-broker-ooms", out.Hits[0].ID)
}

func TestWikiSearch_KeywordRanksByMatch(t *testing.T) {
	t.Parallel()
	srv := &Server{vaultPath: seedVault(t)}
	_, out, err := srv.wikiSearch(context.Background(), nil, wikiSearchIn{Query: "OOM"})
	require.NoError(t, err, "wikiSearch")
	require.NotEmpty(t, out.Hits, "expected INC-1 first")
	require.Equal(t, "inc-1-broker-ooms", out.Hits[0].ID, "expected INC-1 first")
}

func TestWikiGet_LoadsIncident(t *testing.T) {
	t.Parallel()
	srv := &Server{vaultPath: seedVault(t)}
	_, out, err := srv.wikiGet(context.Background(), nil, wikiGetIn{Path: "entries/inc-1-broker-ooms.md"})
	require.NoError(t, err, "wikiGet")
	assert.NotEmpty(t, out.Frontmatter.Title, "missing title")
}

func TestWikiListEntities_FilterByType(t *testing.T) {
	t.Parallel()
	srv := &Server{vaultPath: seedVault(t)}
	_, out, err := srv.wikiListEntities(context.Background(), nil, wikiListEntitiesIn{Type: "service"})
	require.NoError(t, err, "wikiListEntities")
	require.Len(t, out.Entities, 1, "unexpected entities")
	assert.Equal(t, "zeebe-broker", out.Entities[0].Name)
}

func TestWikiSearch_MalformedFilterReturnsError(t *testing.T) {
	t.Parallel()
	srv := &Server{vaultPath: seedVault(t)}
	res, _, err := srv.wikiSearch(context.Background(), nil, wikiSearchIn{
		Filters: wikiSearchFilters{Services: []string{"Zeebe Broker"}},
	})
	require.NoError(t, err, "wikiSearch")
	require.NotNil(t, res, "expected IsError result for malformed name")
	require.True(t, res.IsError, "expected IsError result for malformed name")
}

func TestWikiSearch_ResolutionSurfacedForNearMatch(t *testing.T) {
	t.Parallel()
	srv := &Server{vaultPath: seedVault(t)}
	// "broker" is well-formed but doesn't exact-match any entity.
	// Should produce zero hits but a non-empty resolution that
	// suggests "zeebe-broker".
	_, out, err := srv.wikiSearch(context.Background(), nil, wikiSearchIn{
		Filters: wikiSearchFilters{Services: []string{"broker"}},
	})
	require.NoError(t, err, "wikiSearch")
	require.Len(t, out.Resolution, 1, "expected 1 resolution entry")
	r := out.Resolution[0]
	assert.False(t, r.Exact, "expected exact=false for 'broker'")
	require.NotEmpty(t, r.Near, "expected near=[zeebe-broker, ...]")
	assert.Equal(t, "zeebe-broker", r.Near[0], "expected near=[zeebe-broker, ...]")
}

func TestWikiSearch_ResolutionExactWhenMatch(t *testing.T) {
	t.Parallel()
	srv := &Server{vaultPath: seedVault(t)}
	_, out, err := srv.wikiSearch(context.Background(), nil, wikiSearchIn{
		Filters: wikiSearchFilters{Services: []string{"zeebe-broker"}},
	})
	require.NoError(t, err, "wikiSearch")
	require.Len(t, out.Resolution, 1, "expected exact=true for canonical name")
	assert.True(t, out.Resolution[0].Exact, "expected exact=true for canonical name")
}

func TestWikiSearch_BadStatusEnumRejected(t *testing.T) {
	t.Parallel()
	srv := &Server{vaultPath: seedVault(t)}
	res, _, err := srv.wikiSearch(context.Background(), nil, wikiSearchIn{
		Filters: wikiSearchFilters{Status: []string{"Resolved"}},
	})
	require.NoError(t, err, "wikiSearch")
	require.NotNil(t, res, "expected IsError for bad status")
	require.True(t, res.IsError, "expected IsError for bad status")
}

func TestWikiSearch_BadSeverityEnumRejected(t *testing.T) {
	t.Parallel()
	srv := &Server{vaultPath: seedVault(t)}
	res, _, err := srv.wikiSearch(context.Background(), nil, wikiSearchIn{
		Filters: wikiSearchFilters{Severity: []string{"SEV1"}},
	})
	require.NoError(t, err, "wikiSearch")
	require.NotNil(t, res, "expected IsError for bad severity")
	require.True(t, res.IsError, "expected IsError for bad severity")
}

func TestWikiListEntities_BadTypeRejected(t *testing.T) {
	t.Parallel()
	srv := &Server{vaultPath: seedVault(t)}
	res, _, err := srv.wikiListEntities(context.Background(), nil, wikiListEntitiesIn{Type: "Service"})
	require.NoError(t, err, "wikiListEntities")
	require.NotNil(t, res, "expected IsError for bad type")
	require.True(t, res.IsError, "expected IsError for bad type")
}

func TestWikiGet_RejectsMalformedEntityPath(t *testing.T) {
	t.Parallel()
	srv := &Server{vaultPath: seedVault(t)}
	cases := []string{
		"entities/services/Zeebe Broker.md",
		"entities/services/zeebe_broker.md",
		"entities/Services/zeebe-broker.md",
		"entities/services/zeebe-broker",
		"foo/bar.md",
		"",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			res, _, err := srv.wikiGet(context.Background(), nil, wikiGetIn{Path: p})
			require.NoError(t, err, "wikiGet")
			require.NotNil(t, res, "expected IsError for path %q", p)
			require.True(t, res.IsError, "expected IsError for path %q", p)
		})
	}
}

func TestWikiGet_AcceptsCanonicalPaths(t *testing.T) {
	t.Parallel()
	srv := &Server{vaultPath: seedVault(t)}
	// Both seeded paths should validate-clean (the read after may
	// hit "no such file" if the seed didn't write that exact file —
	// we only care that validateNotePath didn't reject).
	for _, p := range []string{"entries/inc-1-broker-ooms.md", "entities/services/zeebe-broker.md"} {
		t.Run(p, func(t *testing.T) {
			res, _, err := srv.wikiGet(context.Background(), nil, wikiGetIn{Path: p})
			require.NoError(t, err, "wikiGet")
			if res != nil {
				assert.False(t, res.IsError, "unexpected validation error for canonical path %q: %+v", p, res)
			}
		})
	}
}
