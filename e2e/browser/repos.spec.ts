import { expect, test } from "@playwright/test";
import {
  REPO_NAME,
  REPO_OWNER,
  REPO_WITH_SUMMARY,
  gotoAuthed,
  openRepo,
  repoRowInGroup,
  reposGroup,
  repoTestids,
} from "./helpers/triagent";

// Flow 5 browser assertions. The Go harness boots the launcher against the
// "mixed" repos fixture (two linked defaults, two user-local, one of the
// four with a pre-baked summary) and a regenerate stub whose completion is
// gated on a signal the harness releases on a short delay. This spec owns
// the DOM half: the linked + user-local groups render with their repos,
// the summary-present vs empty-state views diverge, and clicking
// regenerate on the empty repo lands a rendered summary. The single-flight
// invariant is pinned server-side in repos_test.go.
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

    // Both groups render. acme/payments + acme/billing are the linked
    // (defaults) repos; acme/gateway + acme/notifier are user-local.
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
    // marker — proving the cache vault was read.
    await openRepo(page, REPO_WITH_SUMMARY);
    const summary = page.getByTestId(repoTestids.summary);
    await expect(summary).toBeVisible({ timeout: 15_000 });
    await expect(summary).toContainText("TRIAGENT-E2E-FLOW5-PAYMENTS-SUMMARY-MARKER");

    // The regenerate target is empty-state: no summary article, the
    // empty-state block instead, with a regenerate button present.
    await openRepo(page, regenTarget);
    await expect(page.getByTestId(repoTestids.emptyState)).toBeVisible({
      timeout: 15_000,
    });
    await expect(page.getByTestId(repoTestids.summary)).toHaveCount(0);
    await expect(page.getByTestId(repoTestids.regenerate)).toBeVisible();
  });

  test("regenerate on the empty repo lands a rendered summary", async ({
    page,
  }) => {
    await openRepo(page, regenTarget);
    await expect(page.getByTestId(repoTestids.emptyState)).toBeVisible({
      timeout: 15_000,
    });

    // Click regenerate. The worker's sub-agent is gated on a signal the
    // harness releases on a short delay; the view flips to in-flight, then
    // — on the success SSE event — re-fetches and renders the summary with
    // the regenerated marker.
    await page.getByTestId(repoTestids.regenerate).click();

    const summary = page.getByTestId(repoTestids.summary);
    await expect(summary).toBeVisible({ timeout: 60_000 });
    await expect(summary).toContainText("TRIAGENT-E2E-FLOW5-REGENERATED-MARKER");
  });
});
