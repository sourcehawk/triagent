import { expect, test } from "@playwright/test";
import {
  findPlaybookCard,
  openPlaybookEditor,
  openPlaybooks,
  playbookProposal,
  playbookProposalBadge,
} from "./helpers/editor";

// Flow 3 browser assertions. The Go harness seeded the
// with-pending-proposal vault (three playbooks + one pending proposal)
// and verified it server-side; this spec opens the playbooks view in the
// embedded SPA and asserts the rendered DOM: the three fixture playbooks
// render as cards, the pending proposal shows its proposed-badge in the
// sidenav, and clicking a card mounts the editor at ?playbook=<id>.
test.describe("playbook editor Flow 3", () => {
  test("lists the fixture playbooks, shows the proposed-badge, mounts the editor", async ({
    page,
  }) => {
    await openPlaybooks(page);

    // The three fixture playbooks each render as a card. (The list also
    // carries the launcher-bundled system metas and paginates, so search
    // each fixture by id rather than asserting a total count.)
    for (const id of ["payments_latency", "broker_ooms", "capture_routing"]) {
      await findPlaybookCard(page, id);
    }

    // The pending proposal surfaces in the sidenav with its proposed
    // (update) badge.
    await expect(playbookProposal(page, "payments_latency")).toBeVisible({
      timeout: 30_000,
    });
    const badge = playbookProposalBadge(page, "payments_latency");
    await expect(badge).toBeVisible();
    await expect(badge).toHaveText(/update/i);

    // Clicking a playbook card navigates to ?playbook=<id> and mounts
    // the editor pane.
    await openPlaybookEditor(page, "broker_ooms");
  });
});
