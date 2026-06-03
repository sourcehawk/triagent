import { describe, it, expect } from "vitest";
import {
  applyEvent,
  isDraftPrToolName,
  PROPOSE_WIKI_DRAFT_TOOL_NAME,
  PROPOSE_PLAYBOOK_DRAFT_TOOL_NAME,
  type EventEnvelope,
  type TranscriptItem,
} from "./events";

function envelope(partial: Partial<EventEnvelope> & Pick<EventEnvelope, "kind" | "seq">): EventEnvelope {
  return {
    timestamp: "2026-05-10T20:00:00Z",
    ...partial,
  } as EventEnvelope;
}

describe("applyEvent — tool_status routing", () => {
  it("attaches tool_status events as nested children of the matching tool_call", () => {
    let items: TranscriptItem[] = [];
    items = applyEvent(items, envelope({
      kind: "tool_use",
      seq: 1,
      toolId: "t1",
      toolName: "mcp__triagent-git-zeebe__draft_pr",
    }));
    items = applyEvent(items, envelope({
      kind: "tool_status",
      seq: 2,
      parentToolId: "t1",
      text: "writing failing test",
    }));
    items = applyEvent(items, envelope({
      kind: "tool_status",
      seq: 3,
      parentToolId: "t1",
      text: "running pytest",
    }));

    expect(items).toHaveLength(1);
    const tc = items[0];
    expect(tc.kind).toBe("tool_call");
    if (tc.kind !== "tool_call") return; // narrow for ts
    expect(tc.children).toHaveLength(2);
    expect(tc.children?.map((c) => c.kind === "status" ? c.text : null)).toEqual([
      "writing failing test",
      "running pytest",
    ]);
  });

  // A proposal drafted inside the wiki_proposal playbook runs in a sub-agent,
  // so its propose_wiki_draft call arrives with a parentToolId (the dispatch).
  // Nesting it would bury the structured result and the inline WikiProposalCard
  // would never render — the exact bug. Proposal-draft calls must escape
  // nesting and surface as a top-level tool_call carrying their result.
  it("hoists a nested propose_wiki_draft call to a top-level tool_call with its result", () => {
    let items: TranscriptItem[] = [];
    // The sub-agent dispatch (e.g. walk_playbook) is the parent.
    items = applyEvent(items, envelope({
      kind: "tool_use",
      seq: 1,
      toolId: "dispatch",
      toolName: "mcp__triagent-strategies__walk_playbook",
    }));
    items = applyEvent(items, envelope({
      kind: "tool_use",
      seq: 2,
      toolId: "sub_propose",
      parentToolId: "dispatch",
      toolName: PROPOSE_WIKI_DRAFT_TOOL_NAME,
    }));
    items = applyEvent(items, envelope({
      kind: "tool_result",
      seq: 3,
      toolId: "sub_propose",
      parentToolId: "dispatch",
      text: '{"kind":"wiki_proposal_draft","proposal_id":"prop-1","slug":"x"}',
    }));

    const card = items.find(
      (it) => it.kind === "tool_call" && it.name === PROPOSE_WIKI_DRAFT_TOOL_NAME,
    );
    expect(card).toBeDefined();
    if (card?.kind !== "tool_call") throw new Error("expected top-level tool_call");
    expect(card.result).toContain("prop-1");
    // It must NOT also be buried as a child of the dispatch.
    const dispatch = items.find((it) => it.kind === "tool_call" && it.toolId === "dispatch");
    if (dispatch?.kind !== "tool_call") throw new Error("expected dispatch tool_call");
    expect(dispatch.children ?? []).toHaveLength(0);
  });

  it("hoists a nested playbook_proposal_draft call the same way", () => {
    let items: TranscriptItem[] = [];
    items = applyEvent(items, envelope({ kind: "tool_use", seq: 1, toolId: "dispatch", toolName: "mcp__triagent-strategies__walk_playbook" }));
    items = applyEvent(items, envelope({ kind: "tool_use", seq: 2, toolId: "sub_pb", parentToolId: "dispatch", toolName: PROPOSE_PLAYBOOK_DRAFT_TOOL_NAME }));
    items = applyEvent(items, envelope({ kind: "tool_result", seq: 3, toolId: "sub_pb", parentToolId: "dispatch", text: '{"proposal_id":"pp-1"}' }));

    const card = items.find((it) => it.kind === "tool_call" && it.name === PROPOSE_PLAYBOOK_DRAFT_TOOL_NAME);
    expect(card?.kind === "tool_call" && card.result).toContain("pp-1");
  });

  it("drops tool_status without parentToolId silently (no crash, no top-level item)", () => {
    const items = applyEvent([], envelope({
      kind: "tool_status",
      seq: 1,
      text: "stray status",
    }));
    expect(items).toEqual([]);
  });

  it("preserves status ordering relative to other nested children", () => {
    let items: TranscriptItem[] = [];
    items = applyEvent(items, envelope({ kind: "tool_use", seq: 1, toolId: "t1", toolName: "x" }));
    items = applyEvent(items, envelope({ kind: "assistant", seq: 2, parentToolId: "t1", text: "thinking..." }));
    items = applyEvent(items, envelope({ kind: "tool_status", seq: 3, parentToolId: "t1", text: "phase A" }));
    items = applyEvent(items, envelope({ kind: "tool_status", seq: 4, parentToolId: "t1", text: "phase B" }));

    const tc = items[0];
    if (tc.kind !== "tool_call") throw new Error("expected tool_call");
    const kinds = tc.children?.map((c) => c.kind);
    expect(kinds).toEqual(["text", "status", "status"]);
  });
});

describe("isDraftPrToolName", () => {
  it("matches per-repo draft_pr tool names", () => {
    expect(isDraftPrToolName("mcp__triagent-git-zeebe__draft_pr")).toBe(true);
    expect(isDraftPrToolName("mcp__triagent-git-operate__draft_pr")).toBe(true);
    expect(isDraftPrToolName("mcp__triagent-git-example-platform__draft_pr")).toBe(true);
  });

  it("rejects other triagent-git tools", () => {
    expect(isDraftPrToolName("mcp__triagent-git-zeebe__analyze_change")).toBe(false);
    expect(isDraftPrToolName("mcp__triagent-git-zeebe__search_issues")).toBe(false);
    expect(isDraftPrToolName("mcp__triagent-git-zeebe__create_github_issue")).toBe(false);
  });

  it("rejects non-triagent-git tools", () => {
    expect(isDraftPrToolName("mcp__triagent-wiki__propose_wiki_draft")).toBe(false);
    expect(isDraftPrToolName("mcp__triagent-strategies__summarize")).toBe(false);
  });

  it("rejects malformed names", () => {
    expect(isDraftPrToolName("")).toBe(false);
    expect(isDraftPrToolName("draft_pr")).toBe(false);
    expect(isDraftPrToolName("triagent-git-zeebe__draft_pr")).toBe(false);
  });
});

describe("applyEvent — codefix_pr_state is a no-op for transcript reducer", () => {
  it("doesn't add a top-level item or attach to a tool_call", () => {
    let items: TranscriptItem[] = [];
    items = applyEvent(items, envelope({
      kind: "codefix_pr_state",
      seq: 1,
      codefixPRState: {
        url: "https://github.com/example-org/example-service/pull/77",
        state: "merged",
      },
    }));
    expect(items).toEqual([]);
  });
});

describe("applyEvent — usage is a no-op for transcript reducer", () => {
  // Regression: the per-API-call `usage` envelope was added without a
  // matching case in applyEvent, so the switch fell through and
  // returned undefined. The next dispatched event then read state as
  // undefined and `[...items, …]` threw "items is not iterable",
  // crashing the session page on snapshot replay.
  it("returns items unchanged so subsequent dispatches keep array state", () => {
    let items: TranscriptItem[] = [];
    items = applyEvent(items, envelope({
      kind: "usage",
      seq: 1,
      usage: { inputTokens: 100, outputTokens: 200 },
    }));
    expect(items).toEqual([]);
    // Dispatching after a usage event must still produce an array.
    items = applyEvent(items, envelope({
      kind: "tool_use",
      seq: 2,
      toolId: "t1",
      toolName: "x",
    }));
    expect(Array.isArray(items)).toBe(true);
    expect(items).toHaveLength(1);
  });
});

// The frontend doesn't run a parse helper over SSE payloads — they flow
// through as EventEnvelope-typed JSON. These tests therefore assert the
// type surface: the new auto-mode fields (origin on user envelopes,
// autoMode on auto_mode_state envelopes) are typed on EventEnvelope and
// the kind discriminator accepts "auto_mode_state". The literals below
// would fail to type-check if events.ts hadn't been extended.
describe("auto_mode_state envelope", () => {
  it("types phase + takenOverBy on the autoMode payload", () => {
    const env: EventEnvelope = envelope({
      seq: 5,
      kind: "auto_mode_state",
      autoMode: { phase: "paused", takenOverBy: "human" },
    });
    expect(env.kind).toBe("auto_mode_state");
    expect(env.autoMode?.phase).toBe("paused");
    expect(env.autoMode?.takenOverBy).toBe("human");
  });

  it("accepts every phase value the Go server emits", () => {
    const phases: Array<NonNullable<EventEnvelope["autoMode"]>["phase"]> = [
      "started",
      "paused",
      "resumed",
      "finished",
      "aborted",
    ];
    for (const phase of phases) {
      const env = envelope({ seq: 1, kind: "auto_mode_state", autoMode: { phase } });
      expect(env.autoMode?.phase).toBe(phase);
    }
  });

  it("carries optional reason / error / warning fields", () => {
    const env = envelope({
      seq: 7,
      kind: "auto_mode_state",
      autoMode: {
        phase: "aborted",
        reason: "consecutive failure cap",
        error: "claude returned non-zero exit",
        warning: false,
      },
    });
    expect(env.autoMode?.reason).toBe("consecutive failure cap");
    expect(env.autoMode?.error).toBe("claude returned non-zero exit");
    expect(env.autoMode?.warning).toBe(false);
  });

  it("emits an auto-mode-divider transcript item so it renders inline in the chat", () => {
    const items = applyEvent([], envelope({
      seq: 9,
      kind: "auto_mode_state",
      autoMode: { phase: "started" },
    }));
    expect(items).toHaveLength(1);
    const it0 = items[0];
    expect(it0.kind).toBe("auto_mode_divider");
    if (it0.kind !== "auto_mode_divider") return; // narrow for ts
    expect(it0.payload.phase).toBe("started");
  });

  it("auto-mode-divider carries takenOverBy + reason through to the transcript item", () => {
    const items = applyEvent([], envelope({
      seq: 10,
      kind: "auto_mode_state",
      autoMode: { phase: "paused", takenOverBy: "human", reason: "operator requested" },
    }));
    expect(items).toHaveLength(1);
    const it0 = items[0];
    if (it0.kind !== "auto_mode_divider") throw new Error("expected divider");
    expect(it0.payload.takenOverBy).toBe("human");
    expect(it0.payload.reason).toBe("operator requested");
  });
});

describe("origin on user envelopes", () => {
  it("retains the operator origin on a typed user envelope", () => {
    const env: EventEnvelope = envelope({
      seq: 6,
      kind: "user",
      origin: "operator",
      text: "wiki",
    });
    expect(env.origin).toBe("operator");
  });

  it("retains the human origin when set explicitly", () => {
    const env = envelope({ seq: 6, kind: "user", origin: "human", text: "hi" });
    expect(env.origin).toBe("human");
  });

  it("user envelopes without an origin still parse (origin is optional)", () => {
    const env = envelope({ seq: 6, kind: "user", text: "legacy" });
    expect(env.origin).toBeUndefined();
  });
});
