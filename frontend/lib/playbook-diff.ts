import type { Playbook, PlaybookNode } from "./playbook";

export function edgeId(source: string, index: number, goto: string): string {
  return `${source}__${index}__${goto}`;
}

export type NodeDiff = "added" | "removed" | "modified" | "unchanged";
export type EdgeDiff = "added" | "removed" | "retargeted" | "unchanged";

export type PlaybookDiff = {
  // keys: union of base.nodes + proposed.nodes
  nodes: Map<string, NodeDiff>;
  // keys: rendered xyflow edge id `${source}__${index}__${goto}`. For
  // unchanged/retargeted/added the key uses the *proposed* goto;
  // for removed the key uses the *base* goto.
  edges: Map<string, EdgeDiff>;
  entrypointChanged: boolean;
  // Per-node list of fields that differ; populated only for "modified".
  modifiedFields: Map<string, string[]>;
};

// Fields whose change flips a node from unchanged → modified. Field
// order also drives the popover ordering.
const NODE_DIFF_FIELDS = [
  "description",
  "suggested_calls",
  "expected_findings",
  "next",
  "terminal_advice",
  "handoff",
] as const satisfies readonly (keyof PlaybookNode)[];

export function diffPlaybooks(
  proposed: Playbook,
  base?: Playbook,
): PlaybookDiff {
  const nodes = new Map<string, NodeDiff>();
  const edges = new Map<string, EdgeDiff>();
  const modifiedFields = new Map<string, string[]>();

  const baseNodes = base?.nodes ?? {};
  const proposedNodes = proposed.nodes;
  const allIds = new Set([
    ...Object.keys(baseNodes),
    ...Object.keys(proposedNodes),
  ]);

  for (const id of allIds) {
    const inBase = id in baseNodes;
    const inProposed = id in proposedNodes;
    if (!inBase && inProposed) {
      nodes.set(id, "added");
      continue;
    }
    if (inBase && !inProposed) {
      nodes.set(id, "removed");
      continue;
    }
    const changed = diffNodeFields(baseNodes[id], proposedNodes[id]);
    if (changed.length === 0) {
      nodes.set(id, "unchanged");
    } else {
      nodes.set(id, "modified");
      modifiedFields.set(id, changed);
    }
  }

  // Edge diff — slot-keyed (source + index) so a retargeted branch is
  // one event, not remove+add. Map keys use the rendered xyflow edge id
  // (matching PlaybookGraph.buildGraph) so the renderer can lookup
  // status with a single Map.get.
  const allSourceIds = new Set([
    ...Object.keys(baseNodes),
    ...Object.keys(proposedNodes),
  ]);
  for (const src of allSourceIds) {
    const baseNexts = baseNodes[src]?.next ?? [];
    const propNexts = proposedNodes[src]?.next ?? [];
    const slotCount = Math.max(baseNexts.length, propNexts.length);
    for (let i = 0; i < slotCount; i++) {
      const b = baseNexts[i];
      const p = propNexts[i];
      if (!b && p) {
        edges.set(edgeId(src, i, p.goto), "added");
      } else if (b && !p) {
        edges.set(edgeId(src, i, b.goto), "removed");
      } else if (b && p) {
        if (b.goto !== p.goto) {
          edges.set(edgeId(src, i, p.goto), "retargeted");
        } else {
          // Condition-only changes don't flip the edge state; the new
          // condition is rendered, the node-level diff already flags
          // the parent as modified.
          edges.set(edgeId(src, i, p.goto), "unchanged");
        }
      }
    }
  }

  return {
    nodes,
    edges,
    entrypointChanged:
      base !== undefined && base.entrypoint !== proposed.entrypoint,
    modifiedFields,
  };
}

function diffNodeFields(a: PlaybookNode, b: PlaybookNode): string[] {
  const out: string[] = [];
  for (const f of NODE_DIFF_FIELDS) {
    if (!deepEqual(a[f], b[f])) out.push(f);
  }
  return out;
}

// JSON-string equality is enough here: node fields are plain data
// (strings, arrays of strings, arrays of small objects). No Date /
// Map / functions in the structure. Cheap and deterministic.
function deepEqual(a: unknown, b: unknown): boolean {
  return JSON.stringify(a ?? null) === JSON.stringify(b ?? null);
}
