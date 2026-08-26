import { describe, it, expect } from "vitest";
import { groupPlaybooks, selectablePlaybooks } from "./playbook-select";
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

describe("groupPlaybooks", () => {
  it("groups by type, investigation first, with the type's description", () => {
    const groups = groupPlaybooks(
      [
        item({ id: "z-general", type: "general" }),
        item({ id: "b-inv", type: "investigation" }),
        item({ id: "a-general", type: "general" }),
        item({ id: "untyped" }),
      ],
      [
        { name: "general", description: "Anything else", source: "system", tracked: true },
        { name: "investigation", description: "Hunt a live incident", source: "system", tracked: true },
      ],
    );
    expect(groups).toEqual([
      { name: "investigation", description: "Hunt a live incident", playbooks: [expect.objectContaining({ id: "b-inv" }), expect.objectContaining({ id: "untyped" })] },
      { name: "general", description: "Anything else", playbooks: [expect.objectContaining({ id: "a-general" }), expect.objectContaining({ id: "z-general" })] },
    ]);
  });

  it("keeps a type unknown to the catalog with an empty description", () => {
    const groups = groupPlaybooks([item({ id: "x", type: "custom" })], []);
    expect(groups).toEqual([{ name: "custom", description: "", playbooks: [expect.objectContaining({ id: "x" })] }]);
  });
});
