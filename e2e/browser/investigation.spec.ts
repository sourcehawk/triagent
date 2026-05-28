import { expect, test } from "@playwright/test";
import {
  INVESTIGATION_ID,
  openInvestigation,
  proposalCardKinds,
  proposalCardOfKind,
  proposalCards,
  proposalKindsInOrder,
  waitForAssistantText,
  waitForProposalCards,
} from "./helpers/triagent";
import { activityPanel, mcpStatusBar } from "./helpers/walkthrough";

// Flow 2 browser assertions. The Go harness has already driven a live
// investigation (four proposals staged, a follow-up turn answered) and
// passed its id in via env; this spec opens the investigation in the real
// embedded SPA and asserts the rendered transcript: four proposal cards in
// order, each with a non-empty preview, and the follow-up turn.
test.describe("investigation Flow 2", () => {
  test.skip(!INVESTIGATION_ID, "no investigation id supplied by the harness");

  test("renders the four proposal cards in order, with previews and a follow-up turn", async ({
    page,
  }) => {
    await openInvestigation(page, INVESTIGATION_ID);

    // All four proposals render as cards in the transcript.
    await waitForProposalCards(page, 4);

    // …in the staged order: playbook, wiki, codefix, github_issue.
    await expect(proposalCards(page)).toHaveCount(4);
    expect(await proposalCardKinds(page)).toEqual([...proposalKindsInOrder]);

    // Each card shows a non-empty body preview (the proposal payload the
    // tool returned), not just an empty frame.
    for (const kind of proposalKindsInOrder) {
      const card = proposalCardOfKind(page, kind);
      await expect(card).toBeVisible();
      const text = (await card.innerText()).trim();
      expect(text.length, `proposal card "${kind}" preview should be non-empty`).toBeGreaterThan(0);
    }

    // The follow-up turn's assistant reply renders.
    await waitForAssistantText(page, "isolated to the payments config");

    // Ambient panels mount alongside the transcript (full coverage is #31's
    // job; this asserts the shared walkthrough helpers resolve their testids
    // against the real SPA).
    await expect(mcpStatusBar(page)).toBeVisible();
    await expect(activityPanel(page)).toBeVisible();
  });
});
