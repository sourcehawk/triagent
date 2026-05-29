import { expect, test } from "@playwright/test";
import { gotoAuthed } from "../helpers/triagent";
import {
  approveWikiProposalInEditor,
  clickNewWikiEntry,
  fillAndSubmitNewWikiEntry,
  openWikiEditor,
  openWikiEditorChat,
  openWikiEditorWithProposal,
  openWikiEntries,
  wikiEditorChatReply,
  wikiEntryRow,
} from "../helpers/editor";

// Flow 4 operator walkthrough — wikis. The browser drives the whole
// through-line a human would click; the Go harness only seeds prior state
// (the with-links entry + a pending update proposal) and asserts the disk
// round-trip after the browser approves. This is the polymorphic twin of
// the playbooks walkthrough (#32): both pin the same editor.Session
// surface — list → create → open editor → chat → AI proposal → accept —
// over a different Subject.Kind, so the shared editor.ts helpers carry the
// selectors-as-contract across both.
//
// The harness passes the seeded slug + pending-proposal id through env.
const SLUG = process.env.TRIAGENT_WIKI_SLUG ?? "";
const PROPOSAL_ID = process.env.TRIAGENT_WIKI_PROPOSAL_ID ?? "";

test.describe("wiki editor Flow 4 walkthrough", () => {
  test.skip(!SLUG, "no wiki slug supplied by the harness");

  test("seed → create → open → chat → proposal → accept", async ({ page }) => {
    // ── 1. Seed: the with-links entry the harness committed into the
    //    vault renders as a row in the entries list. ────────────────────
    await openWikiEntries(page);
    await expect(wikiEntryRow(page, SLUG)).toBeVisible({ timeout: 30_000 });

    // ── 2. Create: drive the real create flow through the sidebar "+ new
    //    wiki entry" trigger + NewWikiEntryModal. Submitting opens an
    //    editor session for the brand-new slug and routes into the wiki
    //    editor — the create flow's contract. The entry only materialises
    //    on disk once a draft is approved (covered for the seeded entry in
    //    step 4), so here we pin that the modal round-trips into a mounted
    //    editor for the new slug rather than asserting a vault row that the
    //    create step alone never writes. ──────────────────────────────────
    const newSlug = "inc-9001-created-via-ui";
    await clickNewWikiEntry(page);
    await fillAndSubmitNewWikiEntry(page, newSlug);
    // Lands in the wiki editor for the new slug, with the chat drawer
    // auto-opened (the backend returns an is_stub entry, so WikiEditor
    // opens chat for the operator to drive the first draft).
    await expect(page).toHaveURL(
      new RegExp(`/wiki/entries/?\\?slug=${newSlug.replace(/[-]/g, "\\$&")}`),
    );
    await expect(page.getByTestId("triagent-wiki-editor")).toBeVisible({
      timeout: 30_000,
    });

    // ── 3. Open the seeded entry's editor + chat: a real claude-stub
    //    round-trip over the wiki subject (the editor.Session polymorphism
    //    the playbook flow exercised over a playbook subject). Opening the
    //    drawer create-or-resumes the editor session; the stub emits its
    //    reply, which renders in the chat transcript. Back to /wiki first —
    //    step 2 left us in the new entry's editor. ────────────────────────
    await openWikiEntries(page);
    await openWikiEditor(page, SLUG);
    await openWikiEditorChat(page);
    // .first(): the editor replays its stub on every turn, so the reply can
    // render more than once — assert "the agent replied" without a
    // strict-mode violation when multiple turns have landed.
    await expect(wikiEditorChatReply(page, "WIKI-EDITOR-STUB-REPLY").first()).toBeVisible({
      timeout: 30_000,
    });

    // ── 4. AI proposal renders → accept → file lands on disk. The harness
    //    seeded a pending UPDATE proposal for the seeded entry; deep-linking
    //    ?proposal=<id>&tab=proposal hydrates it into the editor's "AI
    //    proposal" tab without a scripted sub-agent. Approving promotes the
    //    draft into the vault — the Go side asserts the promoted body + the
    //    surviving links frontmatter on disk after this run. ───────────────
    expect(PROPOSAL_ID, "harness must supply TRIAGENT_WIKI_PROPOSAL_ID").not.toBe(
      "",
    );
    await openWikiEditorWithProposal(page, SLUG, PROPOSAL_ID);
    // The seeded update draft introduces this marker into the Root cause
    // section — its presence in the refetched body proves the promote.
    await approveWikiProposalInEditor(page, "PROPOSED-ROOT-CAUSE-EDIT");

    // The seeded entry is still listed after the promote (the approve
    // rewrote it in place, didn't remove it).
    await gotoAuthed(page, "/wiki");
    await expect(wikiEntryRow(page, SLUG)).toBeVisible({ timeout: 30_000 });
  });
});
