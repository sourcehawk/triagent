import { describe, it, expect } from "vitest";
import { dumpPlaybookYAML, type Playbook, type PlaybookListItem } from "./playbook";
import { extractOutgoing, findInverse } from "./playbook-relations";

function pb(over: Partial<Playbook> & { nodes: Playbook["nodes"]; id: string }): Playbook {
  return {
    symptom: "s",
    entrypoint: Object.keys(over.nodes)[0] ?? "n1",
    ...over,
  };
}

function listItem(playbook: Playbook): PlaybookListItem {
  return {
    id: playbook.id,
    source: "user",
    nodeCount: Object.keys(playbook.nodes).length,
    yaml: dumpPlaybookYAML(playbook),
    syncState: { status: "synced", reason: "test" },
  };
}

describe("extractOutgoing", () => {
  it("returns empty arrays for a playbook with no outgoing links", () => {
    const p = pb({
      id: "a",
      nodes: { start: { description: "d" } },
    });
    expect(extractOutgoing(p)).toEqual({ delegates: [], handoffs: [] });
  });

  it("collects delegate_to from non-terminal nodes", () => {
    const p = pb({
      id: "a",
      nodes: {
        start: { description: "d", delegate_to: "helper", next: [{ condition: "always", goto: "end" }] },
        end: { description: "e", terminal_advice: "done" },
      },
    });
    expect(extractOutgoing(p).delegates).toEqual(["helper"]);
    expect(extractOutgoing(p).handoffs).toEqual([]);
  });

  it("collects handoff from terminal nodes", () => {
    const p = pb({
      id: "a",
      nodes: {
        start: { description: "d", terminal_advice: "go", handoff: ["b", "c"] },
      },
    });
    expect(extractOutgoing(p).handoffs).toEqual(["b", "c"]);
  });

  it("dedupes when the same id appears in multiple nodes", () => {
    const p = pb({
      id: "a",
      nodes: {
        n1: { description: "d", terminal_advice: "x", handoff: ["b"] },
        n2: { description: "e", terminal_advice: "y", handoff: ["b", "c"] },
      },
    });
    expect(extractOutgoing(p).handoffs.sort()).toEqual(["b", "c"]);
  });

  it("captures both delegates and handoffs when present", () => {
    const p = pb({
      id: "a",
      nodes: {
        n1: { description: "d", delegate_to: "helper", next: [{ condition: "always", goto: "n2" }] },
        n2: { description: "e", terminal_advice: "done", handoff: ["other"] },
      },
    });
    expect(extractOutgoing(p)).toEqual({ delegates: ["helper"], handoffs: ["other"] });
  });
});

describe("findInverse", () => {
  it("returns empty arrays when no other playbook references the target", () => {
    const a = pb({ id: "a", nodes: { n: { description: "d" } } });
    const b = pb({ id: "b", nodes: { n: { description: "d" } } });
    expect(findInverse([listItem(a), listItem(b)], "a"))
      .toEqual({ delegatedFrom: [], handoffsFrom: [] });
  });

  it("finds playbooks that delegate to the target", () => {
    const a = pb({
      id: "a",
      nodes: {
        n1: { description: "d", delegate_to: "target", next: [{ condition: "always", goto: "n2" }] },
        n2: { description: "e", terminal_advice: "done" },
      },
    });
    const target = pb({ id: "target", nodes: { n: { description: "x" } } });
    expect(findInverse([listItem(a), listItem(target)], "target").delegatedFrom).toEqual(["a"]);
  });

  it("finds playbooks whose terminal handoffs include the target", () => {
    const a = pb({
      id: "a",
      nodes: { n: { description: "d", terminal_advice: "go", handoff: ["target", "other"] } },
    });
    const target = pb({ id: "target", nodes: { n: { description: "x" } } });
    expect(findInverse([listItem(a), listItem(target)], "target").handoffsFrom).toEqual(["a"]);
  });

  it("excludes self from inverse list (a playbook that handoffs to itself isn't surfaced)", () => {
    const a = pb({
      id: "a",
      nodes: { n: { description: "d", terminal_advice: "go", handoff: ["a"] } },
    });
    expect(findInverse([listItem(a)], "a")).toEqual({ delegatedFrom: [], handoffsFrom: [] });
  });

  it("skips entries whose yaml fails to parse", () => {
    const broken: PlaybookListItem = {
      id: "broken",
      source: "broken",
      nodeCount: 0,
      yaml: "::: not yaml",
      syncState: { status: "synced", reason: "test" },
    };
    const linker = pb({
      id: "linker",
      nodes: { n: { description: "d", terminal_advice: "go", handoff: ["target"] } },
    });
    expect(findInverse([broken, listItem(linker)], "target").handoffsFrom).toEqual(["linker"]);
  });

  it("reports a single playbook in BOTH lists when it both delegates and handoffs to the target", () => {
    const both = pb({
      id: "both",
      nodes: {
        n1: { description: "d", delegate_to: "target", next: [{ condition: "always", goto: "n2" }] },
        n2: { description: "e", terminal_advice: "done", handoff: ["target"] },
      },
    });
    const out = findInverse([listItem(both)], "target");
    expect(out.delegatedFrom).toEqual(["both"]);
    expect(out.handoffsFrom).toEqual(["both"]);
  });
});
