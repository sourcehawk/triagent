import { describe, it, expect } from "vitest";
import { matchesFilter, type StreamFilter } from "./stream-dispatch";
import type { StreamEnvelope } from "./api";

const env = (over: Partial<StreamEnvelope>): StreamEnvelope => ({
  seq: 1,
  kind: "assistant",
  timestamp: "2026-05-10T00:00:00Z",
  ...over,
});

describe("matchesFilter", () => {
  it("global filter matches envelopes with no scope id", () => {
    const f: StreamFilter = { scope: "global" };
    expect(matchesFilter(env({}), f)).toBe(true);
    expect(matchesFilter(env({ investigationId: "x" }), f)).toBe(false);
    expect(matchesFilter(env({ editorSessionId: "y" }), f)).toBe(false);
  });

  it("investigation filter matches by id", () => {
    const f: StreamFilter = { scope: "investigation", id: "x" };
    expect(matchesFilter(env({ investigationId: "x" }), f)).toBe(true);
    expect(matchesFilter(env({ investigationId: "y" }), f)).toBe(false);
    expect(matchesFilter(env({ editorSessionId: "x" }), f)).toBe(false);
    expect(matchesFilter(env({}), f)).toBe(false);
  });

  it("editorSession filter matches by id", () => {
    const f: StreamFilter = { scope: "editorSession", id: "e1" };
    expect(matchesFilter(env({ editorSessionId: "e1" }), f)).toBe(true);
    expect(matchesFilter(env({ editorSessionId: "e2" }), f)).toBe(false);
    expect(matchesFilter(env({ investigationId: "e1" }), f)).toBe(false);
    expect(matchesFilter(env({}), f)).toBe(false);
  });

  it("anyInvestigation filter matches every investigation-scoped envelope", () => {
    const f: StreamFilter = { scope: "anyInvestigation" };
    expect(matchesFilter(env({ investigationId: "x" }), f)).toBe(true);
    expect(matchesFilter(env({ investigationId: "y" }), f)).toBe(true);
    expect(matchesFilter(env({ editorSessionId: "e1" }), f)).toBe(false);
    expect(matchesFilter(env({}), f)).toBe(false);
  });
});
