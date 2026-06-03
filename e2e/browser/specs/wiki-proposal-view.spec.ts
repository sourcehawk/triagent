import { expect, test } from "@playwright/test";
import { gotoAuthed } from "../helpers/triagent";
import { editorTestids } from "../helpers/editor";

// A pending proposal for a BRAND-NEW wiki entry (no entry with that slug on
// disk) must open from its sidenav deep-link. The link the sidebar produces is
// /wiki/entries/?slug=<slug>&proposal=<pid>&tab=proposal. The wiki backend
// returns a synthetic is_stub entry instead of 404, so the editor mounts and
// hydrates the proposal into its AI-proposal tab — the Approve button renders.
const slug = process.env.TRIAGENT_WIKI_SLUG ?? "";
const proposalID = process.env.TRIAGENT_WIKI_PROPOSAL_ID ?? "";

test.describe("new-wiki-entry proposal view", () => {
  test("opens a brand-new wiki entry proposal from its deep-link", async ({
    page,
  }) => {
    expect(slug, "TRIAGENT_WIKI_SLUG must be set").not.toBe("");
    expect(proposalID, "TRIAGENT_WIKI_PROPOSAL_ID must be set").not.toBe("");

    await gotoAuthed(
      page,
      `/wiki/entries/?slug=${encodeURIComponent(slug)}&proposal=${encodeURIComponent(
        proposalID,
      )}&tab=proposal`,
    );

    // The editor mounted (no "entry not found") and the proposal hydrated into
    // its AI-proposal tab — the proposed content is viewable.
    await expect(page.getByTestId(editorTestids.wikiEditor)).toBeVisible({
      timeout: 30_000,
    });
    await expect(
      page.getByText("A brand-new wiki entry proposed by the agent", {
        exact: false,
      }),
    ).toBeVisible({ timeout: 30_000 });
  });
});
