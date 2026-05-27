import { describe, it, expect } from "vitest";
import type { TranscriptItem } from "./events";
import { buildLatestPayloadsByKey } from "./proposal-projection";

const TOOL = "mcp__triagent-strategies__playbook_proposal_draft";

type TestPayload = { proposal_id: string; key_field: string };

// A minimal parser that mirrors what the playbook editor will pass:
// require both fields as strings, dedupe by key_field.
function parseTest(raw: unknown): { key: string; payload: TestPayload } | null {
  const r = raw as Partial<TestPayload>;
  if (typeof r?.proposal_id !== "string" || typeof r?.key_field !== "string") {
    return null;
  }
  return { key: r.key_field, payload: { proposal_id: r.proposal_id, key_field: r.key_field } };
}

function toolCall(toolId: string, name: string, result?: unknown): TranscriptItem {
  return {
    kind: "tool_call",
    id: `tc-${toolId}`,
    toolId,
    name,
    startedAt: "2026-05-08T00:00:00Z",
    result: result === undefined ? undefined : JSON.stringify(result),
  };
}

describe("buildLatestPayloadsByKey", () => {
  it("returns empty when transcript has no matching tool calls", () => {
    const items: TranscriptItem[] = [
      { kind: "user", id: "u1", text: "hi" },
      toolCall("t1", "mcp__triagent-strategies__validate_playbook", { ok: true }),
    ];
    expect(buildLatestPayloadsByKey(items, TOOL, parseTest)).toEqual([]);
  });

  it("returns one entry per distinct key, preserving first-seen order", () => {
    const items: TranscriptItem[] = [
      toolCall("t1", TOOL, { proposal_id: "p1", key_field: "foo" }),
      toolCall("t2", TOOL, { proposal_id: "p2", key_field: "bar" }),
    ];
    const out = buildLatestPayloadsByKey(items, TOOL, parseTest);
    expect(out.map((p) => p.key_field)).toEqual(["foo", "bar"]);
    expect(out.map((p) => p.proposal_id)).toEqual(["p1", "p2"]);
  });

  it("latest-wins when re-drafted for the same key; slot order preserved", () => {
    const items: TranscriptItem[] = [
      toolCall("t1", TOOL, { proposal_id: "p1", key_field: "foo" }),
      toolCall("t2", TOOL, { proposal_id: "p2", key_field: "bar" }),
      toolCall("t3", TOOL, { proposal_id: "p3", key_field: "foo" }),
    ];
    const out = buildLatestPayloadsByKey(items, TOOL, parseTest);
    expect(out.map((p) => p.key_field)).toEqual(["foo", "bar"]);
    expect(out.find((p) => p.key_field === "foo")?.proposal_id).toBe("p3");
  });

  it("skips tool calls without a result (still pending)", () => {
    const items: TranscriptItem[] = [
      toolCall("t1", TOOL, undefined),
      toolCall("t2", TOOL, { proposal_id: "p2", key_field: "bar" }),
    ];
    const out = buildLatestPayloadsByKey(items, TOOL, parseTest);
    expect(out.map((p) => p.key_field)).toEqual(["bar"]);
  });

  it("skips unparseable JSON results", () => {
    const items: TranscriptItem[] = [
      {
        kind: "tool_call",
        id: "tc-bad",
        toolId: "bad",
        name: TOOL,
        startedAt: "2026-05-08T00:00:00Z",
        result: "not json",
      },
      toolCall("t2", TOOL, { proposal_id: "p2", key_field: "ok" }),
    ];
    expect(buildLatestPayloadsByKey(items, TOOL, parseTest).map((p) => p.key_field))
      .toEqual(["ok"]);
  });

  it("drops payloads the parser rejects", () => {
    const items: TranscriptItem[] = [
      toolCall("t1", TOOL, { proposal_id: "p1" }),  // no key_field — parser returns null
      toolCall("t2", TOOL, { key_field: "lonely" }), // no proposal_id — parser returns null
      toolCall("t3", TOOL, { proposal_id: "p3", key_field: "ok" }),
    ];
    expect(buildLatestPayloadsByKey(items, TOOL, parseTest).map((p) => p.key_field))
      .toEqual(["ok"]);
  });

  it("filters out tool calls that don't match the requested tool name", () => {
    const items: TranscriptItem[] = [
      toolCall("t1", "mcp__triagent-wiki__propose_wiki_draft", {
        proposal_id: "w1",
        key_field: "ignored",
      }),
      toolCall("t2", TOOL, { proposal_id: "p2", key_field: "ok" }),
    ];
    expect(buildLatestPayloadsByKey(items, TOOL, parseTest).map((p) => p.key_field))
      .toEqual(["ok"]);
  });
});
