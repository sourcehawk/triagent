import { expect, test } from "@playwright/test";
import {
  REPO_NAME,
  REPO_OWNER,
  REPO_WITH_SUMMARY,
  BASE_URL,
  gotoAuthed,
  openRepo,
  repoActivityPanel,
  repoActivityRow,
  repoActivityRows,
  repoRowInGroup,
  reposGroup,
  repoTestids,
} from "./helpers/triagent";

// Flow 5 — the repos operator walkthrough, browser-driven end to end.
//
// The Go harness boots the launcher against the "mixed" repos fixture:
// two linked defaults (acme/payments, acme/billing), two user-local
// repos (acme/gateway, acme/notifier), exactly one (acme/payments) with
// a pre-baked summary, a seeded local clone for the regenerate target
// (acme/gateway) so the summary worker runs offline, and two seeded
// codefix proposals scoped to acme/gateway so the activity panel has
// agent-opened issues/PRs to render. The regenerate worker's claude
// sub-agent is gated on a signal the harness releases only after this
// spec run returns (Browser.Run is synchronous) — deterministic, with no
// release-vs-probe timing race.
//
// This spec drives the flow from the DOM: land on /repos and read the
// reconciled groups, see the summary-present vs empty-state divergence,
// read the RepoActivityPanel, then regenerate the empty repo — observing
// the in-flight window (button disabled, "generating…") and proving
// single-flight (a concurrent refresh POST returns 409). The regenerated
// summary rendering is asserted in repos-regen-result.spec.ts, which the
// harness runs after releasing the gate and waiting for completion.
test.describe("repos Flow 5", () => {
  const regenTarget = `${REPO_OWNER}/${REPO_NAME}`;

  test.skip(
    !REPO_OWNER || !REPO_NAME || !REPO_WITH_SUMMARY,
    "no repos env supplied by the harness",
  );

  test("renders linked + user-local groups with their repos", async ({
    page,
  }) => {
    await gotoAuthed(page, "/repos");

    // Four-source reconciliation as rendered: the profile's linked_repos
    // surface under "defaults", the user_repos.yaml under "user".
    await expect(reposGroup(page, "defaults")).toBeVisible();
    await expect(reposGroup(page, "user")).toBeVisible();

    await expect(repoRowInGroup(page, "defaults", "acme/payments")).toBeVisible();
    await expect(repoRowInGroup(page, "defaults", "acme/billing")).toBeVisible();
    await expect(repoRowInGroup(page, "user", "acme/gateway")).toBeVisible();
    await expect(repoRowInGroup(page, "user", "acme/notifier")).toBeVisible();
  });

  test("summary-present repo renders its summary; empty repo shows empty state", async ({
    page,
  }) => {
    // The pre-baked repo renders its summary markdown, carrying the seed
    // marker — proving the cache vault was read, not synthesized.
    await openRepo(page, REPO_WITH_SUMMARY);
    const summary = page.getByTestId(repoTestids.summary);
    await expect(summary).toBeVisible({ timeout: 15_000 });
    await expect(summary).toContainText("TRIAGENT-E2E-FLOW5-PAYMENTS-SUMMARY-MARKER");

    // The regenerate target is empty-state: no summary article, the
    // empty-state block instead, with a refresh button present.
    await openRepo(page, regenTarget);
    await expect(page.getByTestId(repoTestids.emptyState)).toBeVisible({
      timeout: 15_000,
    });
    await expect(page.getByTestId(repoTestids.summary)).toHaveCount(0);
    await expect(page.getByTestId(repoTestids.regenerate)).toBeVisible();
  });

  test("activity panel renders the agent-opened issues + PRs for the focused repo", async ({
    page,
  }) => {
    // Focus the regenerate target; the sidenav RepoActivityPanel scopes
    // itself to acme/gateway via ?repo=… and lists the two seeded
    // proposals: an issue+PR row and an issue-only row.
    await openRepo(page, regenTarget);
    await expect(repoActivityPanel(page)).toBeVisible({ timeout: 15_000 });

    // Both seeded proposals render as rows, newest-first per the list
    // handler's sort.
    await expect(repoActivityRows(page)).toHaveCount(2);

    // The issue+PR proposal surfaces its PR link; the issue-only one its
    // issue link with the "no PR" marker.
    const prRow = repoActivityRow(page, "prop-gateway-pr-1");
    await expect(prRow).toBeVisible();
    await expect(prRow).toContainText("PR #42");
    await expect(prRow).toContainText("#41");

    const issueRow = repoActivityRow(page, "prop-gateway-issue-2");
    await expect(issueRow).toBeVisible();
    await expect(issueRow).toContainText("issue #43");
    await expect(issueRow).toContainText("no PR");
  });

  test("regenerate is single-flight; the completed summary renders", async ({
    page,
  }) => {
    await openRepo(page, regenTarget);
    const regenerate = page.getByTestId(repoTestids.regenerate);
    await expect(page.getByTestId(repoTestids.emptyState)).toBeVisible({
      timeout: 15_000,
    });
    await expect(regenerate).toBeEnabled();
    await expect(regenerate).toHaveText("refresh");

    // Click refresh. The worker's sub-agent is gated on a signal the
    // harness releases only AFTER this run returns (see below); the button
    // flips to the disabled in-flight state, proving the launcher admitted
    // the generation and the UI observed it.
    await regenerate.click();
    await expect(regenerate).toHaveText("generating…", { timeout: 15_000 });
    await expect(regenerate).toBeDisabled();

    // Single-flight: while the first worker is gated, a concurrent
    // refresh POST is rejected with 409 (no second worker admitted).
    // page.request shares the browser context's auth cookie, so this is
    // the same authenticated surface the disabled button guards. The
    // status endpoint corroborates the in-flight state independently.
    const refresh = await page.request.post(
      `${BASE_URL}/api/repos/${REPO_OWNER}/${REPO_NAME}/summary/refresh`,
      { data: { kind: "freeform" } },
    );
    expect(refresh.status()).toBe(409);

    const status = await page.request.get(
      `${BASE_URL}/api/repos/${REPO_OWNER}/${REPO_NAME}/summary/status`,
    );
    expect(status.status()).toBe(200);
    expect(((await status.json()) as { inFlight: boolean }).inFlight).toBe(true);

    // Leave the worker gated and end the run here. Because Browser.Run is
    // synchronous, the Go harness releases the gate signal only after this
    // run returns — i.e. after the 409 + in-flight assertions above have
    // provably completed — so there is no release-vs-probe race. The
    // regenerated summary is asserted in repos-regen-result.spec.ts, run
    // after the release + completion.
  });
});
