import { expect, test } from "@playwright/test";
import { REPO_NAME, REPO_OWNER, openRepo, repoTestids } from "./helpers/triagent";

// Flow 5 — regenerated-summary result (deterministic second phase).
//
// The Go harness runs this spec AFTER repos.spec.ts (whose single-flight
// test leaves the regenerate worker gated post-409) has returned, the gate
// signal has been released, and the worker has completed on the Go side.
// By the time this runs the summary is persisted, so the assertion is a
// plain DOM read with no release-vs-render race — this is what replaced the
// in-spec wait-for-release that raced the single-flight probe on slow CI.
test.describe("repos Flow 5 — regenerated summary", () => {
  const regenTarget = `${REPO_OWNER}/${REPO_NAME}`;

  test.skip(!REPO_OWNER || !REPO_NAME, "no repos env supplied by the harness");

  test("the completed regenerated summary renders with its marker", async ({
    page,
  }) => {
    await openRepo(page, regenTarget);
    const summary = page.getByTestId(repoTestids.summary);
    await expect(summary).toBeVisible({ timeout: 30_000 });
    await expect(summary).toContainText("TRIAGENT-E2E-FLOW5-REGENERATED-MARKER");
  });
});
