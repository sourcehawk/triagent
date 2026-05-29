import { expect, test } from "@playwright/test";
import {
  proposalCardKinds,
  proposalCardOfKind,
  proposalCards,
  proposalKindsInOrder,
  waitForAssistantText,
  waitForProposalCards,
} from "./helpers/triagent";
import {
  activityRows,
  clickNewInvestigation,
  composerSend,
  fillAndSubmitInvestigationForm,
  gotoRoot,
  investigationRow,
  investigationRows,
  mcpChip,
  waitForUsageReadout,
} from "./helpers/walkthrough";

// Flow 2, the investigations operator walkthrough: the browser drives the
// whole golden path rather than reading a transcript the Go harness pre-baked.
// It seeds off the fixture investigation, creates a fresh one through the real
// InvestigationForm, watches the kickoff turn render (assistant reply →
// summary block → the four proposal cards in order), verifies the ambient
// panels (active MCP chips, activity rows, the token/cost readout), then sends
// a follow-up and confirms the second turn answers. Backend invariants
// (allowed-tools, system prompt, gh issue body, transcript order) stay pinned
// in TestInvestigation_BackendInvariants; this spec owns the rendered SPA.

// The id the in-progress session fixture seeds; deterministic, so the spec
// asserts the seed row by id without the harness threading it through env.
const SEED_INVESTIGATION_ID = "inv-flow2-fixture";

test.describe("investigation Flow 2 walkthrough", () => {
  test("seeds, creates, drives the kickoff turn, and answers a follow-up", async ({
    page,
  }) => {
    // ── Seed ───────────────────────────────────────────────────────────
    // The pre-baked fixture investigation is listed in the sidebar before
    // any live work.
    await gotoRoot(page);
    await expect(investigationRow(page, SEED_INVESTIGATION_ID)).toBeVisible({
      timeout: 30_000,
    });
    const seedCount = await investigationRows(page).count();

    // ── Create ─────────────────────────────────────────────────────────
    // Open the new-investigation form, fill the profile's one input (the
    // optional "Notes" textarea), and run preflight. The
    // with-prompts-and-linked-repo profile makes every input optional, so a
    // value here is exercising the field, not satisfying a requirement.
    await clickNewInvestigation(page);
    await fillAndSubmitInvestigationForm(page, {
      Notes: "payments pods are crash-looping after the latest rollout",
    });

    // Preflight pushes to /investigations/?id=<new>; the new session view
    // mounts and SessionWorkspace auto-starts the kickoff turn. Wait for the
    // composer (only present on a mounted session view) to confirm we landed.
    await expect(page.getByTestId("triagent-composer-input")).toBeVisible({
      timeout: 30_000,
    });

    // The new investigation now appears in the sidebar alongside the seed.
    await gotoRoot(page);
    await expect
      .poll(async () => investigationRows(page).count(), { timeout: 30_000 })
      .toBeGreaterThan(seedCount);
    // Re-open the freshly-created session (gotoRoot navigated away from it).
    const newId = await investigationRows(page)
      .evaluateAll(
        (els, seed) =>
          els
            .map((el) => el.getAttribute("data-investigation-id") ?? "")
            .filter((id) => id && id !== seed),
        SEED_INVESTIGATION_ID,
      )
      .then((ids) => ids[0]);
    expect(newId, "a non-seed investigation row should exist").toBeTruthy();
    await investigationRow(page, newId!).click();

    // ── Ambient: active MCP chips ────────────────────────────────────────
    // The profile wired a linked repo, which surfaces as the
    // triagent-git-payments chip in the status bar; the core strategies
    // server (the walker the kickoff turn drives) is always present. The
    // profile's reference-mode extra MCP (org-docs) is wired into the
    // agent's allowed-tools but isn't spawned as a server entry, so
    // activeMCPs doesn't render a chip for it — the backend test pins that
    // allowed-tools wiring instead (assertAllowedToolsCover).
    await expect(mcpChip(page, "triagent-git-payments")).toBeVisible({
      timeout: 30_000,
    });
    await expect(mcpChip(page, "triagent-strategies")).toBeVisible();

    // ── Operate: the kickoff turn renders ────────────────────────────────
    // Assistant opener, then the summary block (the amber card the
    // strategies summarize tool drives — labelled "summary", carrying the
    // verdict markdown), then the four proposal cards.
    await waitForAssistantText(page, "Staging four follow-ups");
    await expect(
      page
        .locator('[data-testid="triagent-transcript-list"]')
        .getByText("crash-loops the pods", { exact: false })
        .first(),
    ).toBeVisible({ timeout: 30_000 });

    await waitForProposalCards(page, 4);
    await expect(proposalCards(page)).toHaveCount(4);
    expect(await proposalCardKinds(page)).toEqual([...proposalKindsInOrder]);
    for (const kind of proposalKindsInOrder) {
      const card = proposalCardOfKind(page, kind);
      await expect(card).toBeVisible();
      const text = (await card.innerText()).trim();
      expect(
        text.length,
        `proposal card "${kind}" preview should be non-empty`,
      ).toBeGreaterThan(0);
    }

    // ── Ambient: activity panel + usage readout ──────────────────────────
    // Every proposal round-trip lands as a tool-call row in the activity
    // panel (the summarize call plus the four proposals → at least five).
    await expect
      .poll(async () => activityRows(page).count(), { timeout: 30_000 })
      .toBeGreaterThanOrEqual(5);

    // The kickoff turn carried usage + cost, so the readout renders non-zero.
    await waitForUsageReadout(page);
    const readout = (
      await page.getByTestId("triagent-usage-readout").innerText()
    ).trim();
    expect(readout, "usage readout should show a token tally").toMatch(/tok/);
    expect(readout, "usage readout should show a non-zero token tally").not.toMatch(
      /^0 tok/,
    );

    // ── Follow-up turn ───────────────────────────────────────────────────
    // The operator asks a follow-up via the composer; the resumed session
    // answers with a single assistant reply.
    await composerSend(
      page,
      "Confirm the regression is isolated to the payments config.",
    );
    await waitForAssistantText(page, "isolated to the payments config");
  });
});
