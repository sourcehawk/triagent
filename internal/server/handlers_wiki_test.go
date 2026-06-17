package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWikiProposalResolutionRoundTrip mirrors the playbook-side test
// for the wiki resolution marker: missing → ok=false; written →
// round-trip equality.
func TestWikiProposalResolutionRoundTrip(t *testing.T) {
	t.Parallel()
	proposals := t.TempDir()

	_, ok, err := readWikiProposalResolution(proposals, "prop-aaaaaaaaaaaa")
	require.NoError(t, err, "expected no error for missing marker")
	require.False(t, ok, "expected ok=false for missing marker")

	approved := wikiProposalResolution{
		Outcome:      "approved",
		Slug:         "inc-42",
		Path:         "entries/inc-42-foo.md",
		Commit:       "abc1234",
		StubsCreated: []string{"team:platform"},
		At:           "2026-05-07T11:00:00Z",
	}
	require.NoError(t, writeWikiProposalResolution(proposals, "prop-aaaaaaaaaaaa", approved))
	_, err = os.Stat(filepath.Join(proposals, ".resolved", "prop-aaaaaaaaaaaa.json"))
	require.NoError(t, err, "marker file missing")
	got, ok, err := readWikiProposalResolution(proposals, "prop-aaaaaaaaaaaa")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, approved.Outcome, got.Outcome)
	assert.Equal(t, approved.Slug, got.Slug)
	assert.Equal(t, approved.Path, got.Path)
	assert.Equal(t, approved.Commit, got.Commit)
	require.Len(t, got.StubsCreated, 1, "stubs round-trip lost")
	assert.Equal(t, "team:platform", got.StubsCreated[0])

	// Declined marker can carry the operator's pushback so a later
	// sub-agent walk (via list_proposals / dispatch prompt injection)
	// adjusts to the note instead of re-submitting the same shape.
	declinedWithNote := wikiProposalResolution{
		Outcome: "declined",
		At:      "2026-05-07T11:10:00Z",
		Note:    "merge into the existing INC-41 entry rather than a new one",
	}
	require.NoError(t, writeWikiProposalResolution(proposals, "prop-dddddddddddd", declinedWithNote))
	got, ok, err = readWikiProposalResolution(proposals, "prop-dddddddddddd")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "merge into the existing INC-41 entry rather than a new one", got.Note,
		"decline note must round-trip for the wiki path too")
}

// TestHandleDeclineWikiProposal_PersistsNoteFromBody mirrors the
// playbook-side test: the wiki decline endpoint must accept the
// operator's refinement note in an optional { note } JSON body and
// persist it to the .resolved marker so future dispatches see it.
func TestHandleDeclineWikiProposal_PersistsNoteFromBody(t *testing.T) {
	t.Parallel()
	proposals := t.TempDir()
	mcpBin := writeStubMCPBinary(t)
	a := &apiHandlers{opts: Options{WikiProposalsPath: proposals, MCPBinaryPath: mcpBin}}

	body := strings.NewReader(`{"note":"merge into existing INC-41 entry"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/wiki-proposals/prop-aaaaaaaaaaaa/decline", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "prop-aaaaaaaaaaaa")
	rec := httptest.NewRecorder()
	a.handleDeclineWikiProposal(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code, "body %s", rec.Body.String())

	got, ok, err := readWikiProposalResolution(proposals, "prop-aaaaaaaaaaaa")
	require.NoError(t, err)
	require.True(t, ok, "decline endpoint must still write the resolution marker")
	assert.Equal(t, "declined", got.Outcome)
	assert.Equal(t, "merge into existing INC-41 entry", got.Note,
		"note from POST body must be persisted to the .resolved marker")
}

// TestHandleDeclineWikiProposal_NoBodyStillWorks preserves the legacy
// body-less contract.
func TestHandleDeclineWikiProposal_NoBodyStillWorks(t *testing.T) {
	t.Parallel()
	proposals := t.TempDir()
	mcpBin := writeStubMCPBinary(t)
	a := &apiHandlers{opts: Options{WikiProposalsPath: proposals, MCPBinaryPath: mcpBin}}

	req := httptest.NewRequest(http.MethodPost, "/api/wiki-proposals/prop-eeeeeeeeeeee/decline", nil)
	req.SetPathValue("id", "prop-eeeeeeeeeeee")
	rec := httptest.NewRecorder()
	a.handleDeclineWikiProposal(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	got, ok, err := readWikiProposalResolution(proposals, "prop-eeeeeeeeeeee")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "declined", got.Outcome)
	assert.Empty(t, got.Note, "no body → no note")
}

// TestHandleGetWikiProposal_ResolvesFromLedger ensures the wiki GET
// handler surfaces the approved outcome instead of 404 once the draft
// has been pushed into the vault.
func TestHandleGetWikiProposal_ResolvesFromLedger(t *testing.T) {
	t.Parallel()
	proposals := t.TempDir()
	a := &apiHandlers{opts: Options{WikiProposalsPath: proposals}}

	require.NoError(t, writeWikiProposalResolution(proposals, "prop-aaaaaaaaaaaa", wikiProposalResolution{
		Outcome: "approved",
		Slug:    "inc-42",
		Path:    "entries/inc-42-foo.md",
		Commit:  "abc1234",
		At:      "2026-05-07T11:00:00Z",
	}), "seed")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/wiki-proposals/prop-aaaaaaaaaaaa", nil)
	req.SetPathValue("id", "prop-aaaaaaaaaaaa")
	a.handleGetWikiProposal(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body %s", rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "approved", body["status"])
	assert.Equal(t, "inc-42", body["slug"])
	assert.Equal(t, "entries/inc-42-foo.md", body["path"])
}

// TestWikiBundleAutoIncludePaths_ScopesToEntryWikilinks is a regression
// for the bug where pushing a PR for one wiki entry silently bundled
// every other locally-committed-but-unpushed wiki file (entries +
// their entity stubs) into the same PR. The auto-include must walk
// only [[wikilinks]] in the resolved entries; other unsynced entries
// stay in their own PR.
func TestWikiBundleAutoIncludePaths_ScopesToEntryWikilinks(t *testing.T) {
	t.Parallel()
	entryA := []byte(`---
schema_version: 1
id: inc-a
date: 2026-05-12
title: A
status: open
services: []
errors: []
symptoms: []
---

## Summary

References [[service-x]] and [[error-y]].

## Root cause

## Fix
`)
	entryBContent := []byte(`---
schema_version: 1
id: inc-b
date: 2026-05-12
title: B
status: open
services: []
errors: []
symptoms: []
---

## Summary

References [[service-z]].

## Root cause

## Fix
`)
	_ = entryBContent // documents that B exists in unsynced but is intentionally NOT in entries

	// Vault state per `git diff --name-only origin/HEAD HEAD` includes A,
	// B, and every entity stub that was locally committed. Only A is in
	// the resolved bundle.
	unsynced := map[string]bool{
		"entries/inc-a.md":              true,
		"entries/inc-b.md":              true,
		"entities/services/service-x.md": true,
		"entities/errors/error-y.md":    true,
		"entities/services/service-z.md": true, // referenced only by B
	}
	resolved := map[string]bool{"entries/inc-a.md": true}
	entries := []wikiEntryBody{{rel: "entries/inc-a.md", content: entryA}}

	got := wikiBundleAutoIncludePaths(entries, unsynced, resolved)
	assert.ElementsMatch(t,
		[]string{"entities/services/service-x.md", "entities/errors/error-y.md"},
		got,
		"auto-include must capture stubs referenced by A and only those",
	)
	for _, leak := range []string{"entries/inc-b.md", "entities/services/service-z.md"} {
		assert.NotContains(t, got, leak,
			"auto-include leaked unrelated unsynced file %q into A's bundle", leak)
	}
}

// TestWikiBundleAutoIncludePaths_SkipsAlreadyIncluded — a stub the
// caller already resolved (e.g. because the operator opened it in the
// editor and it landed in the explicit file list) must not appear in
// the auto-include list a second time.
func TestWikiBundleAutoIncludePaths_SkipsAlreadyIncluded(t *testing.T) {
	t.Parallel()
	entry := []byte(`---
schema_version: 1
id: inc-a
date: 2026-05-12
title: A
status: open
services: []
errors: []
symptoms: []
---

## Summary

References [[service-x]].

## Root cause

## Fix
`)
	unsynced := map[string]bool{
		"entries/inc-a.md":              true,
		"entities/services/service-x.md": true,
	}
	resolved := map[string]bool{
		"entries/inc-a.md":              true,
		"entities/services/service-x.md": true, // already in the explicit bundle
	}
	got := wikiBundleAutoIncludePaths(
		[]wikiEntryBody{{rel: "entries/inc-a.md", content: entry}},
		unsynced, resolved,
	)
	assert.Empty(t, got, "service-x is already in the bundle; auto-include must not re-add it")
}

// TestWikiBundleAutoIncludePaths_SkipsSyncedStubs — a referenced stub
// that's already on origin (i.e. NOT in the unsynced set) must not be
// re-applied: the side branch is built from origin, so the stub is
// already there with origin's content. Reapplying it would mark it as
// modified even when no change was intended.
func TestWikiBundleAutoIncludePaths_SkipsSyncedStubs(t *testing.T) {
	t.Parallel()
	entry := []byte(`---
schema_version: 1
id: inc-a
date: 2026-05-12
title: A
status: open
services: []
errors: []
symptoms: []
---

## Summary

References [[service-x]] (already on origin) and [[service-new]] (local-only).

## Root cause

## Fix
`)
	unsynced := map[string]bool{
		"entries/inc-a.md":                true,
		"entities/services/service-new.md": true,
	}
	resolved := map[string]bool{"entries/inc-a.md": true}
	got := wikiBundleAutoIncludePaths(
		[]wikiEntryBody{{rel: "entries/inc-a.md", content: entry}},
		unsynced, resolved,
	)
	assert.Equal(t, []string{"entities/services/service-new.md"}, got,
		"auto-include must pick up the unsynced stub and ignore the already-synced one")
}

// TestUnsyncedWikiFiles_SubdirVaultPath verifies that unsyncedWikiFiles
// returns paths relative to vaultPath even when vaultPath is a
// subdirectory of the git repo root (e.g. wiki_path: "wikis/"). Without
// --relative, git diff emits paths like "wikis/entries/foo.md" while
// callers look up vault-relative keys like "entries/foo.md" — causing
// every newly committed entry to appear synced and disabling the push
// button.
func TestUnsyncedWikiFiles_SubdirVaultPath(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	gitInit(t, repoRoot)

	// Write a seed file at the repo root so the initial commit is non-empty.
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("wiki\n"), 0o600))
	gitCommitAll(t, repoRoot, "init")
	gitSetupOriginWithMain(t, repoRoot)

	// Create the wikis/ subdir that mirrors a profile with wiki_path: "wikis".
	vaultPath := filepath.Join(repoRoot, "wikis")
	require.NoError(t, os.MkdirAll(filepath.Join(vaultPath, "entries"), 0o700))

	// Commit a new entry locally (not yet pushed to origin).
	entryPath := filepath.Join(vaultPath, "entries", "inv-test.md")
	require.NoError(t, os.WriteFile(entryPath, []byte("# test\n"), 0o600))
	gitCommitAll(t, repoRoot, "wiki: inv-test")

	got := unsyncedWikiFiles(context.Background(), vaultPath)

	// The key must be vault-relative ("entries/inv-test.md"), not
	// repo-root-relative ("wikis/entries/inv-test.md").
	assert.True(t, got["entries/inv-test.md"],
		"entry committed under wikis/ subdir must appear as vault-relative key in unsynced set; got keys: %v", got)
	assert.False(t, got["wikis/entries/inv-test.md"],
		"repo-root-relative key must not appear; got keys: %v", got)
}
