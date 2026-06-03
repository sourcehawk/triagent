import { expect, test } from "@playwright/test";
import {
  proposalCardOfKind,
  waitForAssistantText,
  waitForProposalCards,
} from "../helpers/triagent";
import {
  clickNewInvestigation,
  fillAndSubmitInvestigationForm,
  gotoRoot,
} from "../helpers/walkthrough";

// Proposals drafted inside a walk_playbook sub-agent dispatch arrive nested
// (their tool-events carry parentToolId). The transcript folder hoists the
// wiki + playbook proposal results out of that nesting so their inline
// approve/decline cards still render in the session view — the regression
// this pins. The nested codefix card is NOT hoisted (codefix surfaces on the
// repos activity panel instead, pinned in TestProposalSurfacing_NestedBackendInvariants),
// so the transcript shows exactly the two hoisted cards.
test.describe("nested sub-agent proposal surfacing", () => {
  test("hoists wiki + playbook cards out of the dispatch nesting", async ({
    page,
  }) => {
    await gotoRoot(page);
    await clickNewInvestigation(page);
    await fillAndSubmitInvestigationForm(page, {
      Notes: "nested-proposal surfacing check",
    });

    // Preflight mounts the session view (composer present) and SessionWorkspace
    // auto-starts the kickoff turn that stages the nested proposals.
    await expect(page.getByTestId("triagent-composer-input")).toBeVisible({
      timeout: 30_000,
    });
    await waitForAssistantText(page, "guided proposal sub-agents");

    // Exactly two inline cards — wiki and playbook — even though both were
    // drafted inside the dispatch sub-agent. The sub-agent also made a
    // validation-failed playbook draft (proposal_id:""); that must NOT render
    // as an empty card, so the playbook card count is exactly one.
    await waitForProposalCards(page, 2);
    await expect(proposalCardOfKind(page, "wiki")).toBeVisible();
    await expect(proposalCardOfKind(page, "playbook")).toHaveCount(1);
  });
});
