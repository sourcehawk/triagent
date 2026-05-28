import { expect, type Locator, type Page } from "@playwright/test";
import { gotoAuthed } from "./triagent";

// Shared operator-walkthrough steps the four flow specs (#31-34) build on.
// The pattern each flow follows is seed → create → operate → verify-ambient;
// these helpers are the reusable verbs for those steps, wrapping the
// walkthrough testids (the contract #30 ships, kept in lockstep with the
// data-testid attributes in frontend/components).
//
// Reuses gotoAuthed / BASE_URL / TOKEN from triagent.ts — this module never
// re-derives the launcher URL or the auth token.

// walkthroughTestids are the data-testid values #30 adds to the real
// components. Keep these in lockstep with:
//   - Sidebar.tsx        (investigation row + new-investigation entry)
//   - MCPStatusBar.tsx   (wrapper + per-server chip)
//   - ActivityPanel.tsx  (wrapper + per-call row)
//   - SessionView.tsx    (UsageReadout + composer input/send)
export const walkthroughTestids = {
  // Sidebar investigation row; data-investigation-id carries the id.
  investigationRow: "triagent-investigation-row",
  // The "+ new investigation" sidebar link.
  newInvestigation: "triagent-new-investigation",
  // MCPStatusBar wrapper; one per session view.
  mcpStatusBar: "triagent-mcp-status-bar",
  // One per active MCP server chip; data-server-alias carries the alias.
  mcpChip: "triagent-mcp-chip",
  // ActivityPanel wrapper (the right-hand <aside>).
  activityPanel: "triagent-activity-panel",
  // One per tool-call row in the activity panel; data-tool-id carries the
  // claude tool_use id.
  activityRow: "triagent-activity-row",
  // The per-session token + cost readout above the composer.
  usageReadout: "triagent-usage-readout",
  // The follow-up composer textarea.
  composerInput: "triagent-composer-input",
  // The composer send button.
  composerSend: "triagent-composer-send",
} as const;

// gotoRoot lands on the launcher root (the investigations list), auth-primed.
export async function gotoRoot(page: Page): Promise<void> {
  await gotoAuthed(page, "/");
}

// ── Sidebar: seed + create ───────────────────────────────────────────

// investigationRows returns every investigation row in the sidebar, in DOM
// order. The seed step asserts prior state by counting / matching these.
export function investigationRows(page: Page): Locator {
  return page.getByTestId(walkthroughTestids.investigationRow);
}

// investigationRow returns the sidebar row for a specific investigation id.
export function investigationRow(page: Page, id: string): Locator {
  return page.locator(
    `[data-testid="${walkthroughTestids.investigationRow}"][data-investigation-id="${id}"]`,
  );
}

// clickNewInvestigation clicks the "+ new investigation" sidebar entry,
// taking the operator to the new-investigation form.
export async function clickNewInvestigation(page: Page): Promise<void> {
  await page.getByTestId(walkthroughTestids.newInvestigation).click();
}

// fillAndSubmitInvestigationForm fills the schema-driven InvestigationForm
// by field label and clicks "run preflight". The form's scalar inputs are
// label-wrapped, so getByLabel resolves each by its profile-defined label;
// `inputs` maps that label to the value to type. Submitting drives the real
// preflight path the same way an operator clicking the button does.
export async function fillAndSubmitInvestigationForm(
  page: Page,
  inputs: Record<string, string>,
): Promise<void> {
  for (const [label, value] of Object.entries(inputs)) {
    await page.getByLabel(label, { exact: false }).first().fill(value);
  }
  await page.getByRole("button", { name: /run preflight/i }).click();
}

// ── Ambient panels: MCP status bar ───────────────────────────────────

// mcpStatusBar returns the MCPStatusBar wrapper for the current session view.
export function mcpStatusBar(page: Page): Locator {
  return page.getByTestId(walkthroughTestids.mcpStatusBar);
}

// mcpChips returns every active-MCP chip, in DOM order.
export function mcpChips(page: Page): Locator {
  return mcpStatusBar(page).getByTestId(walkthroughTestids.mcpChip);
}

// mcpChip returns the chip for a specific server alias.
export function mcpChip(page: Page, alias: string): Locator {
  return mcpStatusBar(page).locator(
    `[data-testid="${walkthroughTestids.mcpChip}"][data-server-alias="${alias}"]`,
  );
}

// ── Ambient panels: activity panel ───────────────────────────────────

// activityPanel returns the ActivityPanel wrapper for the current session.
export function activityPanel(page: Page): Locator {
  return page.getByTestId(walkthroughTestids.activityPanel);
}

// activityRows returns every tool-call row in the activity panel, in DOM
// order.
export function activityRows(page: Page): Locator {
  return activityPanel(page).getByTestId(walkthroughTestids.activityRow);
}

// activityRow returns the activity row for a specific tool_use id.
export function activityRow(page: Page, toolId: string): Locator {
  return activityPanel(page).locator(
    `[data-testid="${walkthroughTestids.activityRow}"][data-tool-id="${toolId}"]`,
  );
}

// ── Ambient panels: usage readout ────────────────────────────────────

// usageReadout returns the per-session token + cost readout. It renders only
// once the launcher has folded non-zero usage / cost (a fresh session hides
// it), so a spec waits on it rather than asserting synchronously.
export function usageReadout(page: Page): Locator {
  return page.getByTestId(walkthroughTestids.usageReadout);
}

// ── Operate: composer ────────────────────────────────────────────────

// composerInput returns the follow-up composer textarea.
export function composerInput(page: Page): Locator {
  return page.getByTestId(walkthroughTestids.composerInput);
}

// composerSend sends a follow-up message: fills the composer with text and
// clicks send. Mirrors the operator typing a follow-up and pressing the send
// chip.
export async function composerSend(page: Page, text: string): Promise<void> {
  await composerInput(page).fill(text);
  await page.getByTestId(walkthroughTestids.composerSend).click();
}

// waitForUsageReadout waits until the usage readout has rendered — i.e. the
// launcher folded a non-zero token / cost tally from the session's stream.
export async function waitForUsageReadout(page: Page): Promise<void> {
  await expect(usageReadout(page)).toBeVisible({ timeout: 30_000 });
}
