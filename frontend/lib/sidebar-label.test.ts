import { describe, it, expect } from "vitest";
import type { Investigation } from "@/lib/api";
import { labelFor, matchesQuery } from "@/lib/sidebar-label";

function makeInv(overrides: Partial<Investigation> = {}): Investigation {
  return {
    id: "i1",
    namespace: "",
    mcpConfigPath: "",
    sessionDir: "",
    promEnabled: false,
    createdAt: new Date().toISOString(),
    started: false,
    streaming: false,
    archived: false,
    syncState: { status: "local-only" },
    ...overrides,
  } as Investigation;
}

describe("labelFor", () => {
  it("uses the trimmed label when present", () => {
    expect(labelFor(makeInv({ label: "  hello  " }))).toEqual({
      text: "hello",
      placeholder: false,
    });
  });

  it("falls back to a placeholder when label is empty/whitespace/undefined", () => {
    expect(labelFor(makeInv({ label: "" }))).toEqual({
      text: "New Investigation",
      placeholder: true,
    });
    expect(labelFor(makeInv({ label: "   " }))).toEqual({
      text: "New Investigation",
      placeholder: true,
    });
    expect(labelFor(makeInv({}))).toEqual({
      text: "New Investigation",
      placeholder: true,
    });
  });
});

describe("matchesQuery", () => {
  const inv = makeInv({
    label: "OOMKilled in zeebe-broker",
    namespace: "abc-zeebe",
    incidentUrl: "https://incident.io/i/INC-42",
    slackChannelUrl: "https://slack.com/...",
    notes: "operator notes about the OOM",
  });

  it("returns true for empty query", () => {
    expect(matchesQuery(inv, "")).toBe(true);
  });

  it.each([
    ["oom", true],
    ["OOM", true],
    ["abc-zeebe", true],
    ["INC-42", true],
    ["operator", true],
    ["unrelated", false],
  ])("query %p → %p", (q, want) => {
    expect(matchesQuery(inv, q)).toBe(want);
  });
});
