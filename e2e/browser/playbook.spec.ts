import { expect, test } from "@playwright/test";
import {
  acceptProposal,
  closePlaybookChat,
  createPlaybookFromYAML,
  editPlaybookSymptom,
  findPlaybookCard,
  openPlaybookChat,
  openPlaybookEditor,
  openPlaybooks,
  openProposalPreview,
  playbookProposal,
  playbookProposalBadge,
  savePlaybookEdit,
  sendEditorChat,
} from "./helpers/editor";

// Flow 3 — the playbook editor operator walkthrough. The Go harness
// seeded the with-pending-proposal vault (three playbooks + one pending
// proposal for payments_latency) and verified it server-side; the
// browser then drives the whole operator through-line a human would
// click, end to end:
//
//   seed   → land on /playbooks, see the three fixture playbooks + the
//            pending-proposal badge.
//   create → "+ new playbook" → paste-YAML modal → the new playbook
//            appears in the list.
//   operate→ open a playbook → chat with the editor agent → the agent
//            replies → re-open the pending proposal → its diff renders
//            in the AI-proposal tab → accept it → the proposal clears
//            from the sidenav ledger.
//   verify → a manual symptom edit saves and round-trips (the field
//            holds the edit across a reload, proving it persisted).
//
// Backend invariants (disk round-trip, ledger .resolved marker, the
// real triagent-mcp write path) are pinned in playbook_test.go; this
// spec is the rendered-DOM through-line over the same vault.
test.describe("playbook editor Flow 3 — operator walkthrough", () => {
  test("seed → create → chat + accept proposal → manual edit", async ({
    page,
  }) => {
    // ── Seed: the three fixture playbooks render; the one with a
    // pending proposal shows its proposed (update) badge. ──────────
    await openPlaybooks(page);
    for (const id of ["payments_latency", "broker_ooms", "capture_routing"]) {
      await findPlaybookCard(page, id);
    }
    await expect(playbookProposal(page, "payments_latency")).toBeVisible({
      timeout: 30_000,
    });
    const badge = playbookProposalBadge(page, "payments_latency");
    await expect(badge).toBeVisible();
    await expect(badge).toHaveText(/update/i);

    // ── Create: drive the sidebar "+ new playbook" → paste-YAML modal.
    // A successful save routes to the editor for the new id; navigating
    // back to /playbooks then surfaces it as a card in the list. ────
    const newID = "e2e_walkthrough_pb";
    const newYAML = [
      `id: ${newID}`,
      "schema_version: 1",
      'symptom: "e2e walkthrough seed playbook"',
      "entrypoint: start",
      "nodes:",
      "  start:",
      '    description: "first step"',
      "    next:",
      '      - condition: "done"',
      "        goto: terminal_done",
      "  terminal_done:",
      '    description: "Done."',
      '    terminal_advice: "all clear"',
      "",
    ].join("\n");
    await createPlaybookFromYAML(page, newID, newYAML);
    await openPlaybooks(page);
    await findPlaybookCard(page, newID);

    // ── Operate: open a fixture playbook, chat with the editor agent,
    // and confirm the stub's reply lands in the transcript. ─────────
    await openPlaybookEditor(page, "payments_latency");
    await openPlaybookChat(page);
    await sendEditorChat(page, "review this playbook for me");
    await expect(
      page.getByText(/PLAYBOOK-EDITOR-STUB-REPLY/),
    ).toBeVisible({ timeout: 30_000 });

    // Collapse the chat drawer — it's bottom-anchored fixed-position and
    // overlays the proposal tab body, so it would intercept the approve
    // click. Collapsing keeps the session alive.
    await closePlaybookChat(page);

    // Re-open the pending proposal from the sidenav: it deep-links into
    // the editor's AI-proposal tab, hydrating ProposalCard with the
    // diff. Accepting it clears the proposal from the ledger.
    await openProposalPreview(page, "payments_latency");
    await acceptProposal(page);
    await expect(playbookProposal(page, "payments_latency")).toHaveCount(0, {
      timeout: 30_000,
    });

    // ── Verify: a manual symptom edit saves and round-trips. Reload
    // the editor from the server and confirm the edited symptom
    // survives — the save reached disk via the real write path. ─────
    const editedSymptom = "Payments p99 breaching SLO (edited via e2e walkthrough)";
    await editPlaybookSymptom(page, editedSymptom);
    await savePlaybookEdit(page);
    // Re-fetch from the server: go back to the list, re-open the editor,
    // and confirm the edited symptom survives the round-trip.
    await openPlaybooks(page);
    await openPlaybookEditor(page, "payments_latency");
    await expect(page.getByTestId("triagent-playbook-symptom")).toHaveValue(
      editedSymptom,
      { timeout: 30_000 },
    );
  });
});
