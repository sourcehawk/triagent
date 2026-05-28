import { expect, type Locator, type Page } from "@playwright/test";
import { gotoAuthed } from "./triagent";

// Shared selectors + waits for the polymorphic editor surface (Flow 3
// playbooks + Flow 4 wiki). Both editors mount off a URL search param,
// list their subjects in a sidenav/grid, surface a proposed-badge, and
// open a per-subject editor pane — the same shape over two subject
// kinds, so the selectors live here and both specs reuse them.
//
// Testid convention mirrors triagent.ts: data-testid="triagent-<component>-<role>".
// The kind-specific subject id rides as a data-<kind>-id attribute so a
// spec can target one row without depending on copy.
export const editorTestids = {
  // Playbook surface.
  playbookList: "triagent-playbook-list",
  playbookCard: "triagent-playbook-card",
  playbookEditor: "triagent-playbook-editor",
  playbookProposal: "triagent-playbook-proposal",
  playbookProposalBadge: "triagent-playbook-proposal-badge",
  // Wiki surface.
  wikiEntryList: "triagent-wiki-entry-list",
  wikiEntryRow: "triagent-wiki-entry-row",
  wikiEditor: "triagent-wiki-editor",
} as const;

// ── Playbook surface ──────────────────────────────────────────────

// openPlaybooks navigates to the playbooks index (no subject selected).
export async function openPlaybooks(page: Page): Promise<void> {
  await gotoAuthed(page, "/playbooks");
}

// playbookCards returns every playbook card in the main-pane grid, in
// DOM order. The list lives in the main pane (it's grid-laid-out);
// the sidenav carries pending proposals + related links.
export function playbookCards(page: Page): Locator {
  return page.getByTestId(editorTestids.playbookCard);
}

// playbookCard returns the card for a specific playbook id.
export function playbookCard(page: Page, id: string): Locator {
  return page.locator(
    `[data-testid="${editorTestids.playbookCard}"][data-playbook-id="${id}"]`,
  );
}

// waitForPlaybookCards waits until at least `count` cards have rendered
// — the list fetches over REST after mount, so the spec must wait.
export async function waitForPlaybookCards(page: Page, count: number): Promise<void> {
  await expect(playbookCards(page)).toHaveCount(count, { timeout: 30_000 });
}

// playbookProposal returns the sidenav pending-proposal row for a
// playbook id (the "proposed" surface an operator re-opens from).
export function playbookProposal(page: Page, playbookID: string): Locator {
  return page.locator(
    `[data-testid="${editorTestids.playbookProposal}"][data-playbook-id="${playbookID}"]`,
  );
}

// playbookProposalBadge returns the new/update badge inside a pending
// proposal row — the "proposed-badge" the flow asserts is visible.
export function playbookProposalBadge(page: Page, playbookID: string): Locator {
  return playbookProposal(page, playbookID).getByTestId(
    editorTestids.playbookProposalBadge,
  );
}

// openPlaybookEditor clicks a playbook card and waits for the editor
// pane to mount at ?playbook=<id>.
export async function openPlaybookEditor(page: Page, id: string): Promise<void> {
  await playbookCard(page, id).click();
  await expect(page).toHaveURL(new RegExp(`[?&]playbook=${escapeRegExp(id)}(?:&|$)`));
  await expect(page.getByTestId(editorTestids.playbookEditor)).toBeVisible({
    timeout: 30_000,
  });
}

// ── Wiki surface ──────────────────────────────────────────────────

// openWikiEntries navigates to the wiki entries index.
export async function openWikiEntries(page: Page): Promise<void> {
  await gotoAuthed(page, "/wiki/entries");
}

// wikiEntryRows returns every wiki entry row in the list, in DOM order.
export function wikiEntryRows(page: Page): Locator {
  return page.getByTestId(editorTestids.wikiEntryRow);
}

// wikiEntryRow returns the row for a specific entry slug.
export function wikiEntryRow(page: Page, slug: string): Locator {
  return page.locator(
    `[data-testid="${editorTestids.wikiEntryRow}"][data-entry-id="${slug}"]`,
  );
}

// waitForWikiEntryRows waits until at least `count` entry rows render.
export async function waitForWikiEntryRows(page: Page, count: number): Promise<void> {
  await expect(wikiEntryRows(page)).toHaveCount(count, { timeout: 30_000 });
}

// openWikiEditor clicks an entry row and waits for the editor pane to
// mount at ?slug=<slug>.
export async function openWikiEditor(page: Page, slug: string): Promise<void> {
  await wikiEntryRow(page, slug).click();
  await expect(page).toHaveURL(new RegExp(`[?&]slug=${escapeRegExp(slug)}(?:&|$)`));
  await expect(page.getByTestId(editorTestids.wikiEditor)).toBeVisible({
    timeout: 30_000,
  });
}

// escapeRegExp escapes a string for literal use inside a RegExp — slugs
// and ids carry hyphens / underscores that are regex-safe, but the
// helper keeps the URL matcher robust if an id ever carries a dot.
function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
