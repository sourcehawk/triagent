import { describe, it, expect } from "vitest";
import type { Playbook } from "./playbook";
import { diffPlaybooks } from "./playbook-diff";

function pb(over: Partial<Playbook> & { nodes: Playbook["nodes"] }): Playbook {
  return {
    id: "pb",
    symptom: "s",
    entrypoint: Object.keys(over.nodes)[0] ?? "n1",
    ...over,
  };
}

describe("diffPlaybooks — nodes", () => {
  it("treats undefined base as everything-added", () => {
    const proposed = pb({ nodes: { n1: { description: "d" } } });
    const d = diffPlaybooks(proposed);
    expect(d.nodes.get("n1")).toBe("added");
    expect(d.entrypointChanged).toBe(false);
  });

  it("flags identical playbooks as all-unchanged", () => {
    const base = pb({ nodes: { n1: { description: "d" } } });
    const proposed = pb({ nodes: { n1: { description: "d" } } });
    const d = diffPlaybooks(proposed, base);
    expect(d.nodes.get("n1")).toBe("unchanged");
    expect(d.modifiedFields.size).toBe(0);
  });

  it("flags id-only-in-proposed as added", () => {
    const base = pb({ nodes: { n1: { description: "d" } } });
    const proposed = pb({
      nodes: { n1: { description: "d" }, n2: { description: "e" } },
    });
    const d = diffPlaybooks(proposed, base);
    expect(d.nodes.get("n2")).toBe("added");
  });

  it("flags id-only-in-base as removed", () => {
    const base = pb({
      nodes: { n1: { description: "d" }, n2: { description: "e" } },
    });
    const proposed = pb({ nodes: { n1: { description: "d" } } });
    const d = diffPlaybooks(proposed, base);
    expect(d.nodes.get("n2")).toBe("removed");
  });

  it("flags description change as modified with field name", () => {
    const base = pb({ nodes: { n1: { description: "old" } } });
    const proposed = pb({ nodes: { n1: { description: "new" } } });
    const d = diffPlaybooks(proposed, base);
    expect(d.nodes.get("n1")).toBe("modified");
    expect(d.modifiedFields.get("n1")).toEqual(["description"]);
  });

  it("flags suggested_calls change as modified", () => {
    const base = pb({
      nodes: { n1: { description: "d", suggested_calls: [{ tool: "a" }] } },
    });
    const proposed = pb({
      nodes: {
        n1: { description: "d", suggested_calls: [{ tool: "a", args: { x: 1 } }] },
      },
    });
    const d = diffPlaybooks(proposed, base);
    expect(d.nodes.get("n1")).toBe("modified");
    expect(d.modifiedFields.get("n1")).toEqual(["suggested_calls"]);
  });

  it("lists multiple modified fields", () => {
    const base = pb({
      nodes: {
        n1: { description: "old", expected_findings: ["a"], terminal_advice: "x" },
      },
    });
    const proposed = pb({
      nodes: {
        n1: { description: "new", expected_findings: ["b"], terminal_advice: "x" },
      },
    });
    const d = diffPlaybooks(proposed, base);
    expect(d.nodes.get("n1")).toBe("modified");
    expect(d.modifiedFields.get("n1")?.sort()).toEqual([
      "description",
      "expected_findings",
    ]);
  });
});

describe("diffPlaybooks — edges", () => {
  it("flags identical edges as unchanged", () => {
    const base = pb({
      nodes: {
        n1: { description: "d", next: [{ condition: "c", goto: "n2" }] },
        n2: { description: "e" },
      },
    });
    const proposed = pb({
      nodes: {
        n1: { description: "d", next: [{ condition: "c", goto: "n2" }] },
        n2: { description: "e" },
      },
    });
    const d = diffPlaybooks(proposed, base);
    expect(d.edges.get("n1__0__n2")).toBe("unchanged");
  });

  it("flags slot with changed goto as retargeted (one event)", () => {
    const base = pb({
      nodes: {
        n1: { description: "d", next: [{ condition: "c", goto: "n2" }] },
        n2: { description: "e" },
        n3: { description: "f" },
      },
    });
    const proposed = pb({
      nodes: {
        n1: { description: "d", next: [{ condition: "c", goto: "n3" }] },
        n2: { description: "e" },
        n3: { description: "f" },
      },
    });
    const d = diffPlaybooks(proposed, base);
    // Key uses proposed goto.
    expect(d.edges.get("n1__0__n3")).toBe("retargeted");
    // No phantom remove+add for the same slot.
    expect(d.edges.get("n1__0__n2")).toBeUndefined();
  });

  it("flags slot only in proposed as added", () => {
    const base = pb({
      nodes: {
        n1: { description: "d", next: [{ condition: "a", goto: "n2" }] },
        n2: { description: "e" },
      },
    });
    const proposed = pb({
      nodes: {
        n1: {
          description: "d",
          next: [
            { condition: "a", goto: "n2" },
            { condition: "b", goto: "n2" },
          ],
        },
        n2: { description: "e" },
      },
    });
    const d = diffPlaybooks(proposed, base);
    expect(d.edges.get("n1__0__n2")).toBe("unchanged");
    expect(d.edges.get("n1__1__n2")).toBe("added");
  });

  it("flags slot only in base as removed (key uses base goto)", () => {
    const base = pb({
      nodes: {
        n1: {
          description: "d",
          next: [
            { condition: "a", goto: "n2" },
            { condition: "b", goto: "n2" },
          ],
        },
        n2: { description: "e" },
      },
    });
    const proposed = pb({
      nodes: {
        n1: { description: "d", next: [{ condition: "a", goto: "n2" }] },
        n2: { description: "e" },
      },
    });
    const d = diffPlaybooks(proposed, base);
    expect(d.edges.get("n1__1__n2")).toBe("removed");
  });

  it("treats condition-only change on same slot as unchanged-topology", () => {
    const base = pb({
      nodes: {
        n1: { description: "d", next: [{ condition: "old", goto: "n2" }] },
        n2: { description: "e" },
      },
    });
    const proposed = pb({
      nodes: {
        n1: { description: "d", next: [{ condition: "new", goto: "n2" }] },
        n2: { description: "e" },
      },
    });
    const d = diffPlaybooks(proposed, base);
    expect(d.edges.get("n1__0__n2")).toBe("unchanged");
    // Node is still flagged modified (next[] array changed).
    expect(d.nodes.get("n1")).toBe("modified");
  });

  it("when base is undefined, every edge is added", () => {
    const proposed = pb({
      nodes: {
        n1: { description: "d", next: [{ condition: "c", goto: "n2" }] },
        n2: { description: "e" },
      },
    });
    const d = diffPlaybooks(proposed);
    expect(d.edges.get("n1__0__n2")).toBe("added");
  });

  it("when source node only in base, all its edges are removed", () => {
    const base = pb({
      nodes: {
        n1: { description: "d", next: [{ condition: "c", goto: "n2" }] },
        n2: { description: "e" },
      },
    });
    const proposed = pb({ nodes: { n2: { description: "e" } } });
    const d = diffPlaybooks(proposed, base);
    expect(d.edges.get("n1__0__n2")).toBe("removed");
  });
});

describe("diffPlaybooks — entrypoint", () => {
  it("flags entrypoint changes when base provided", () => {
    const base = pb({
      entrypoint: "n1",
      nodes: { n1: { description: "d" }, n2: { description: "e" } },
    });
    const proposed = pb({
      entrypoint: "n2",
      nodes: { n1: { description: "d" }, n2: { description: "e" } },
    });
    const d = diffPlaybooks(proposed, base);
    expect(d.entrypointChanged).toBe(true);
  });

  it("never flags entrypoint change when base is undefined", () => {
    const proposed = pb({
      entrypoint: "n1",
      nodes: { n1: { description: "d" } },
    });
    expect(diffPlaybooks(proposed).entrypointChanged).toBe(false);
  });
});
