//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sourcehawk/triagent/e2e/harness"
)

// TestPlaybookProposalView_NewPlaybookOpensFromSidebar pins the bug where a
// pending proposal for a brand-new playbook (no live playbook with that id)
// failed to open: clicking it deep-linked into the editor, which called
// getPlaybook(<id>) -> 404 and showed "playbook not found". The editor now
// falls back to seeding from the proposal draft. The Go side seeds the vault
// (a proposal with no base playbook) and runs the Playwright spec.
func TestPlaybookProposalView_NewPlaybookOpensFromSidebar(t *testing.T) {
	h := harness.Launch(t, harness.Options{
		Profile:  "minimal",
		Playbook: "with-new-playbook-proposal",
		Browser:  true,
	})
	h.Browser.Run(t, "playbook-proposal-view.spec.ts")
}

// TestWikiProposalView_NewEntryOpens is the wiki twin: a pending proposal for
// a brand-new wiki entry (no entry with that slug on disk) must open from its
// sidenav deep-link. The wiki backend returns a synthetic is_stub entry rather
// than 404, so this protects that the new-entry proposal path keeps working.
func TestWikiProposalView_NewEntryOpens(t *testing.T) {
	const slug = "synthetic-new-wiki-entry"
	const proposalID = "prop-5e7714e10b2c"
	h := harness.Launch(t, harness.Options{
		Profile: "minimal",
		Browser: true,
	})
	seedNewWikiProposal(t, h, "minimal", proposalID, slug)
	if !wikiProposalPending(t, h, proposalID) {
		t.Fatalf("seeded wiki proposal %q not pending server-side before browser run", proposalID)
	}
	h.Browser.SetEnv("TRIAGENT_WIKI_SLUG", slug)
	h.Browser.SetEnv("TRIAGENT_WIKI_PROPOSAL_ID", proposalID)
	h.Browser.Run(t, "wiki-proposal-view.spec.ts")
}

// seedNewWikiProposal writes a draft for a brand-new entry (no base file) into
// the wiki-proposals dir as <proposalID>__<slug>.md — the on-disk shape
// handleListWikiProposals enumerates. Content is written directly (not from a
// fixture) since the slug intentionally has no matching vault entry.
func seedNewWikiProposal(t *testing.T, h *harness.Harness, profile, proposalID, slug string) {
	t.Helper()
	dir := wikiProposalsDir(h, profile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir wiki-proposals dir: %v", err)
	}
	body := "---\n" +
		"schema_version: 1\n" +
		"id: " + slug + "\n" +
		"date: \"2026-06-04\"\n" +
		"title: Synthetic new wiki entry proposal\n" +
		"status: resolved\n" +
		"severity: sev3\n" +
		"---\n\n" +
		"## Summary\n\n" +
		"A brand-new wiki entry proposed by the agent; no entry with this slug exists on disk yet, so opening the proposal must render from the draft.\n\n" +
		"## Root cause\n\nSynthetic.\n"
	if err := os.WriteFile(filepath.Join(dir, proposalID+"__"+slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write new wiki proposal: %v", err)
	}
}
