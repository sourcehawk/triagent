import { expect, test } from "@playwright/test";
import {
  openPlaybooks,
  openProposalPreview,
  playbookProposal,
} from "../helpers/editor";

// A pending proposal for a BRAND-NEW playbook (no live playbook with that id
// on disk) must open from the sidenav. The editor deep-links via
// ?playbook=<id>&proposal=<pid>&tab=proposal; getPlaybook(<id>) 404s because
// nothing's been promoted yet, so the editor has to fall back to seeding from
// the proposal draft instead of showing "playbook not found".
test.describe("new-playbook proposal view", () => {
  test("opens a brand-new playbook proposal from the sidenav", async ({
    page,
  }) => {
    await openPlaybooks(page);

    // The sidenav lists the pending proposal even though no live playbook
    // exists for its id.
    await expect(playbookProposal(page, "new_synthetic_playbook")).toBeVisible({
      timeout: 30_000,
    });

    // Clicking it must open the proposal preview — openProposalPreview asserts
    // the Approve button renders, which only happens if the editor seeded from
    // the proposal draft rather than 404-ing on the missing base playbook.
    await openProposalPreview(page, "new_synthetic_playbook");
  });
});
