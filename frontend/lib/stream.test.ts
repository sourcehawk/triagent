import { describe, it, expect } from "vitest";
import { STREAM_EVENT_KINDS } from "./stream";

// The single EventSource registers one listener per kind (see StreamProvider).
// A kind absent from this list is delivered to no listener and silently
// dropped — its live updates only appear after a /transcript refetch. That is
// exactly how codefix PR-state and auto-mode transitions regressed. Every kind
// with a live consumer MUST be listed; this test pins that contract.
describe("STREAM_EVENT_KINDS", () => {
  // Each entry: the kind and the consumer that subscribes to it live.
  const requiredLiveKinds: Array<[string, string]> = [
    ["auto_mode_state", "useAutoMode (composer gating + phase dividers)"],
    ["codefix_pr_state", "CodefixPRStateProvider (PR lifecycle)"],
    ["repo_summary_state", "RepoSummaryStateProvider"],
    ["wiki_proposal_created", "WikiProposalNotifier (pending-proposals refresh)"],
    ["watch_status", "watches list"],
    ["signal_created", "AllWatchesSignalsPanel"],
    ["item_captured", "watch ingest panels"],
    ["ingest_run_started", "WatchIngestRunsPanel"],
    ["ingest_run_finished", "WatchIngestRunsPanel"],
    // Core transcript kinds folded by applyEvent.
    ["tool_use", "transcript"],
    ["tool_result", "transcript"],
    ["tool_status", "transcript nested status"],
    ["assistant", "transcript"],
    ["user", "transcript"],
  ];

  for (const [kind, consumer] of requiredLiveKinds) {
    it(`registers "${kind}" — consumed by ${consumer}`, () => {
      expect(STREAM_EVENT_KINDS).toContain(kind);
    });
  }

  it("has no duplicate kinds", () => {
    expect(new Set(STREAM_EVENT_KINDS).size).toBe(STREAM_EVENT_KINDS.length);
  });
});
