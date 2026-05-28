import { expect, test } from "@playwright/test";
import { openWikiEditor, openWikiEntries, wikiEntryRow } from "./helpers/editor";

// Flow 4 browser assertions. The Go harness seeded the with-links wiki
// entry into the launcher's git-init'd vault and verified it
// server-side; this spec opens the wiki entries view in the embedded SPA
// and asserts the rendered DOM: the seeded entry renders as a row, and
// clicking it mounts the wiki editor at ?slug=<slug>. Reuses the shared
// editor.ts helpers — the same selectors-as-contract over the wiki
// surface that the playbook spec uses over the playbook surface.
const SLUG = process.env.TRIAGENT_WIKI_SLUG ?? "";

test.describe("wiki editor Flow 4", () => {
  test.skip(!SLUG, "no wiki slug supplied by the harness");

  test("lists the seeded entry and mounts the wiki editor", async ({ page }) => {
    await openWikiEntries(page);

    // The seeded entry renders as a row in the entries list.
    await expect(wikiEntryRow(page, SLUG)).toBeVisible({ timeout: 30_000 });

    // Clicking the row navigates to ?slug=<slug> and mounts the editor.
    await openWikiEditor(page, SLUG);
  });
});
