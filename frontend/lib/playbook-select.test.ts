import { describe, it, expect } from "vitest";
import { selectablePlaybooks } from "./playbook-select";
import type { PlaybookListItem } from "./playbook";

const synced = { status: "synced", reason: "" } as PlaybookListItem["syncState"];

function item(over: Partial<PlaybookListItem> & { id: string }): PlaybookListItem {
  return { source: "plugin", nodeCount: 1, yaml: "", syncState: synced, ...over };
}

describe("selectablePlaybooks", () => {
  it("drops locked, disabled and broken entries", () => {
    const out = selectablePlaybooks([
      item({ id: "investigation", source: "system", locked: true }),
      item({ id: "off", disabled: true }),
      item({ id: "bad", source: "broken" }),
      item({ id: "ok" }),
    ]);
    expect(out.map((p) => p.id)).toEqual(["ok"]);
  });

  it("orders by type (investigation first) then id", () => {
    const out = selectablePlaybooks([
      item({ id: "z-general", type: "general" }),
      item({ id: "b-inv", type: "investigation" }),
      item({ id: "a-general", type: "general" }),
      item({ id: "untyped" }),
    ]);
    expect(out.map((p) => p.id)).toEqual(["b-inv", "untyped", "a-general", "z-general"]);
  });
});
