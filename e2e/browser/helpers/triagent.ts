import { expect, type Page, type Locator } from "@playwright/test";

// The launcher base URL and per-launch token the Go harness passes in via
// env. Specs read them through these helpers rather than process.env
// directly so the wiring stays in one place.
export const BASE_URL = process.env.TRIAGENT_BASE_URL ?? "http://127.0.0.1:8080";
export const TOKEN = process.env.TRIAGENT_TOKEN ?? "";

// The investigation id the harness created for this run (Flow 2 and the
// wave-3 flows that drive a live session). Empty for flows that don't
// pre-create one.
export const INVESTIGATION_ID = process.env.TRIAGENT_INVESTIGATION_ID ?? "";

// Testid convention: data-testid="triagent-<component>-<role>". These
// constants are the contract wave-3 specs write against; keep them in
// lockstep with the data-testid attributes in frontend/components.
export const testids = {
  // The transcript <ul> in SessionView; scope proposal-card queries to it.
  transcriptList: "triagent-transcript-list",
  // One per staged proposal in the transcript. Disambiguated by the
  // data-proposal-kind attribute: playbook | wiki | codefix | github_issue.
  proposalCard: "triagent-proposal-card",
  // An assistant (agent) reply bubble in the transcript.
  assistantMessage: "triagent-assistant-message",
} as const;

// The four proposal kinds, in the order the investigation flow stages
// them. data-proposal-kind on each triagent-proposal-card carries one.
export const proposalKindsInOrder = [
  "playbook",
  "wiki",
  "codefix",
  "github_issue",
] as const;

// gotoAuthed navigates to a launcher path with the launch token attached,
// priming the SPA auth cookie (the launcher 303-redirects to the
// cookie'd canonical URL), then waits for the SPA shell to settle.
export async function gotoAuthed(page: Page, path = "/"): Promise<void> {
  const sep = path.includes("?") ? "&" : "?";
  const url = TOKEN ? `${path}${sep}token=${TOKEN}` : path;
  await page.goto(url, { waitUntil: "domcontentloaded" });
}

// openInvestigation navigates to a specific investigation's view. The URL
// is the source of truth for view state (ADR-0006): /investigations?id=<id>
// mounts the SessionWorkspace (→ SessionView) for it.
export async function openInvestigation(page: Page, id: string): Promise<void> {
  await gotoAuthed(page, `/investigations?id=${id}`);
}

// transcript returns the transcript list locator (the scope for
// proposal-card / assistant-message queries).
export function transcript(page: Page): Locator {
  return page.getByTestId(testids.transcriptList);
}

// proposalCards returns every proposal card in the transcript, in DOM
// (transcript) order.
export function proposalCards(page: Page): Locator {
  return transcript(page).getByTestId(testids.proposalCard);
}

// proposalCardOfKind returns the proposal card for a given kind
// (playbook | wiki | codefix | github_issue). The kind rides as a
// data-proposal-kind attribute on the card element itself.
export function proposalCardOfKind(page: Page, kind: string): Locator {
  return transcript(page).locator(
    `[data-testid="${testids.proposalCard}"][data-proposal-kind="${kind}"]`,
  );
}

// proposalCardKinds returns the data-proposal-kind of every proposal card,
// in transcript order — the contract the ordering assertion checks.
export async function proposalCardKinds(page: Page): Promise<string[]> {
  return proposalCards(page).evaluateAll((els) =>
    els.map((el) => el.getAttribute("data-proposal-kind") ?? ""),
  );
}

// waitForProposalCards waits until at least `count` proposal cards have
// rendered in the transcript. Proposals arrive over SSE after the live
// turn, so the spec must wait rather than assert synchronously.
export async function waitForProposalCards(page: Page, count: number): Promise<void> {
  await expect(proposalCards(page)).toHaveCount(count, { timeout: 30_000 });
}

// waitForAssistantText waits until an assistant reply containing substr
// has rendered. Used to confirm a follow-up turn landed.
export async function waitForAssistantText(page: Page, substr: string): Promise<void> {
  await expect(
    transcript(page).getByTestId(testids.assistantMessage).filter({ hasText: substr }),
  ).toBeVisible({ timeout: 30_000 });
}
