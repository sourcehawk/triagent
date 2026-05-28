//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sourcehawk/triagent/e2e/harness"
)

// Flow 4 — wiki editor, backend invariants. Mirrors the playbook flow
// over the editor's *wiki* subject, pinning that editor.Session is
// genuinely polymorphic: the same /api/editor-sessions code path that
// drove a playbook chat drives a wiki chat, and the same on-disk
// round-trip discipline holds. The wiki edit path round-trips the
// `links` frontmatter through yaml.v3 while preserving the markdown
// body — the durable "frontmatter intact" invariant the editor's
// manual-save path must hold.
//
// The launcher git-inits an empty wiki vault on boot (offline,
// no-upstream); the test seeds the fixture entry into it and commits so
// the edit handler's clean-tree precondition is met. Writes go through
// the real wiki edit endpoint (frontmatter reserialise + git commit).
func TestWikiEditor_BackendInvariants(t *testing.T) {
	h := harness.Launch(t, harness.Options{
		Profile:    "minimal",
		StubScript: "wiki-editor",
	})
	const slug = "inc-4242-payments-latency"
	seedWikiEntry(t, h, "minimal", slug)

	// The seeded entry is listed and readable, with its links
	// frontmatter intact end to end.
	if !wikiEntryListed(t, h, slug) {
		t.Fatalf("seeded wiki entry %q not listed by /api/wiki/entries", slug)
	}
	body := wikiEntryMarkdown(t, h, slug)
	if !strings.Contains(body, "## Root cause") {
		t.Fatalf("wiki entry body missing required section (got %q)", truncate(body))
	}

	// Editor chat over the wiki subject is the same editor.Session code
	// path the playbook flow exercised — the polymorphism contract. Open
	// the session, wait for end, confirm the stub reply landed.
	stream := h.Client.OpenStream(t)
	defer stream.Close()
	sessID := createWikiEditorSession(t, h, slug)
	waitForEditorEnd(t, stream, sessID)
	if !editorTranscriptHasAssistant(t, h, sessID, "WIKI-EDITOR-STUB-REPLY") {
		t.Errorf("wiki editor chat reply not found in transcript for session %s", sessID)
	}

	// Manual edit save: append a marker to the body and PUT it back. The
	// launcher reserialises the frontmatter (preserving the links block)
	// and rewrites the body. The on-disk file reflects the edited body
	// AND retains the links frontmatter.
	const bodyMarker = "Added a saturation alert (edited via e2e manual save)."
	editWikiEntryBody(t, h, slug, body+"\n\n"+bodyMarker+"\n")
	onDisk := readWikiEntryFromDisk(t, h, "minimal", slug)
	if !strings.Contains(onDisk, bodyMarker) {
		t.Errorf("manual edit did not reach disk: %q not in %q", bodyMarker, truncate(onDisk))
	}
	// The links frontmatter survived the reserialise round-trip — the
	// invariant the wiki editor's save path must hold.
	if !strings.Contains(onDisk, "links:") || !strings.Contains(onDisk, "incident_io:") {
		t.Errorf("links frontmatter not retained after manual save (got %q)", truncate(onDisk))
	}
}

// TestWikiEditor_Browser drives the wiki operator walkthrough in the
// browser: the seeded with-links entry is listed; a brand-new entry is
// created through NewWikiEntryModal and routes into the editor; the editor
// chat is a real claude-stub round-trip; and a seeded pending UPDATE
// proposal is approved from the editor's AI-proposal tab. The Go side seeds
// prior state, hands env to the spec, and — once Playwright returns (Run is
// synchronous) — asserts the approve promoted the draft onto the vault file
// and cleared the pending proposal. The polymorphic twin of the playbook
// browser walkthrough over Subject.Kind=wiki.
func TestWikiEditor_Browser(t *testing.T) {
	h := harness.Launch(t, harness.Options{
		Profile:    "minimal",
		StubScript: "wiki-editor",
		Browser:    true,
	})
	const slug = "inc-4242-payments-latency"
	const proposalID = "prop-aaaa1111bbbb"
	seedWikiEntry(t, h, "minimal", slug)
	seedWikiProposal(t, h, "minimal", proposalID, slug)

	if !wikiEntryListed(t, h, slug) {
		t.Fatalf("seeded wiki entry %q missing server-side before browser run", slug)
	}
	if !wikiProposalPending(t, h, proposalID) {
		t.Fatalf("seeded wiki proposal %q not pending server-side before browser run", proposalID)
	}

	h.Browser.SetEnv("TRIAGENT_WIKI_SLUG", slug)
	h.Browser.SetEnv("TRIAGENT_WIKI_PROPOSAL_ID", proposalID)
	h.Browser.Run(t, "wiki.spec.ts")

	// The browser approved the proposal from the AI-proposal tab. Approve
	// promotes the draft into the vault verbatim — the on-disk entry now
	// carries the proposed root-cause edit AND retains the links
	// frontmatter (the wiki entry's promote-from-draft round-trip).
	onDisk := readWikiEntryFromDisk(t, h, "minimal", slug)
	if !strings.Contains(onDisk, "PROPOSED-ROOT-CAUSE-EDIT") {
		t.Errorf("approved proposal did not promote onto the vault entry (got %q)", truncate(onDisk))
	}
	if !strings.Contains(onDisk, "links:") || !strings.Contains(onDisk, "incident_io:") {
		t.Errorf("links frontmatter not retained after promote (got %q)", truncate(onDisk))
	}
	// The pending proposal is cleared once approved.
	if wikiProposalPending(t, h, proposalID) {
		t.Errorf("wiki proposal %q still pending after browser approve", proposalID)
	}
}

// ── wiki REST helpers ─────────────────────────────────────────────

// wikiEntryListed reports whether GET /api/wiki/entries includes the
// given slug.
func wikiEntryListed(t *testing.T, h *harness.Harness, slug string) bool {
	t.Helper()
	status, body := h.Client.Get(t, "/api/wiki/entries?limit=100")
	if status != http.StatusOK {
		t.Fatalf("GET /api/wiki/entries status = %d (body %s)", status, body)
	}
	return strings.Contains(string(body), slug)
}

// wikiEntryMarkdown returns the markdown body for a wiki entry slug.
func wikiEntryMarkdown(t *testing.T, h *harness.Harness, slug string) string {
	t.Helper()
	status, body := decodeWikiEntry(t, h, slug)
	if status != http.StatusOK {
		t.Fatalf("GET wiki entry %s status = %d (body %s)", slug, status, body.raw)
	}
	return body.Markdown
}

type wikiEntryResp struct {
	Markdown string `json:"markdown"`
	raw      string
}

func decodeWikiEntry(t *testing.T, h *harness.Harness, slug string) (int, wikiEntryResp) {
	t.Helper()
	status, raw := h.Client.Get(t, "/api/wiki/entries/"+slug)
	out := wikiEntryResp{raw: string(raw)}
	if status == http.StatusOK {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode wiki entry %s: %v (body %s)", slug, err, raw)
		}
	}
	return status, out
}

// editWikiEntryBody PUTs an updated markdown body and fails on non-200.
func editWikiEntryBody(t *testing.T, h *harness.Harness, slug, markdown string) {
	t.Helper()
	status, body := h.Client.PutJSON(t, "/api/wiki/entries/"+slug, map[string]any{
		"markdown": markdown,
	})
	if status != http.StatusOK {
		t.Fatalf("PUT /api/wiki/entries/%s status = %d (body %s)", slug, status, body)
	}
}

// createWikiEditorSession opens an editor chat session bound to a wiki
// subject and returns its id. Reuses createEditorSession (defined in
// playbook_test.go) — the single REST entry point shared across both
// editor kinds, which is the polymorphism contract under test.
func createWikiEditorSession(t *testing.T, h *harness.Harness, slug string) string {
	t.Helper()
	return createEditorSession(t, h, map[string]any{
		"kind": "wiki",
		"wiki": map[string]any{
			"kind": "entry",
			"id":   slug,
		},
	})
}

// ── disk helpers ──────────────────────────────────────────────────

// wikiDir is the on-disk wiki vault the launcher persisted into.
func wikiDir(h *harness.Harness, profile string) string {
	return filepath.Join(h.StateDir, "triagent", profile, "wiki")
}

// readWikiEntryFromDisk reads the entry file straight off disk so the
// round-trip assertion bypasses the API.
func readWikiEntryFromDisk(t *testing.T, h *harness.Harness, profile, slug string) string {
	t.Helper()
	path := filepath.Join(wikiDir(h, profile), "entries", slug+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read wiki entry from disk %s: %v", path, err)
	}
	return string(data)
}

// seedWikiEntry copies the with-links fixture entry into the launcher's
// git-init'd wiki vault and commits it, leaving the tree clean for the
// edit handler's clean-tree precondition. The launcher boots the vault
// as an empty local git repo (offline, no upstream); seeding post-boot
// means the list/get/edit endpoints — which read the live filesystem —
// pick the entry up without the harness needing to ship a nested .git.
func seedWikiEntry(t *testing.T, h *harness.Harness, profile, slug string) {
	t.Helper()
	vault := wikiDir(h, profile)
	src := filepath.Join("fixtures", "wiki", "with-links", "entries", slug+".md")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read wiki fixture %s: %v", src, err)
	}
	entriesDir := filepath.Join(vault, "entries")
	if err := os.MkdirAll(entriesDir, 0o700); err != nil {
		t.Fatalf("mkdir wiki entries dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(entriesDir, slug+".md"), data, 0o644); err != nil {
		t.Fatalf("write wiki entry into vault: %v", err)
	}
	// Pin a local git identity so the launcher's own commit (on edit)
	// inherits it, then commit the seed so the working tree is clean.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, args := range [][]string{
		{"config", "user.email", "e2e@triagent.local"},
		{"config", "user.name", "triagent-e2e"},
		{"add", "."},
		{"commit", "-m", "e2e: seed wiki entry"},
	} {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", vault}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in wiki vault: %v\n%s", args, err, out)
		}
	}
}

// wikiProposalsDir is the on-disk wiki-proposals dir the launcher reads
// pending drafts from (sibling of the wiki vault under the profile root).
func wikiProposalsDir(h *harness.Harness, profile string) string {
	return filepath.Join(h.StateDir, "triagent", profile, "wiki-proposals")
}

// seedWikiProposal copies the with-links pending-proposal draft into the
// launcher's wiki-proposals dir as <proposalID>__<slug>.md — the on-disk
// shape handleListWikiProposals enumerates and approveWikiByID promotes. A
// "pending" proposal is just a draft file with no sibling resolution
// marker, so dropping the file post-boot is enough for the list/get/approve
// endpoints (which read the live filesystem) to pick it up. This is the
// wiki analogue of the playbook with-pending-proposal fixture; the harness
// has no WikiProposalFixtures knob, so the test seeds it directly.
func seedWikiProposal(t *testing.T, h *harness.Harness, profile, proposalID, slug string) {
	t.Helper()
	src := filepath.Join("fixtures", "wiki", "with-links", "proposals", proposalID+"__"+slug+".md")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read wiki proposal fixture %s: %v", src, err)
	}
	dir := wikiProposalsDir(h, profile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir wiki-proposals dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, proposalID+"__"+slug+".md"), data, 0o644); err != nil {
		t.Fatalf("write wiki proposal into proposals dir: %v", err)
	}
}

// wikiProposalPending reports whether GET /api/wiki-proposals lists the
// given proposal id as pending (no resolution marker yet).
func wikiProposalPending(t *testing.T, h *harness.Harness, proposalID string) bool {
	t.Helper()
	status, body := h.Client.Get(t, "/api/wiki-proposals")
	if status != http.StatusOK {
		t.Fatalf("GET /api/wiki-proposals status = %d (body %s)", status, body)
	}
	var parsed struct {
		Proposals []struct {
			ProposalID string `json:"proposal_id"`
		} `json:"proposals"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode wiki proposals: %v (body %s)", err, body)
	}
	for _, p := range parsed.Proposals {
		if p.ProposalID == proposalID {
			return true
		}
	}
	return false
}
