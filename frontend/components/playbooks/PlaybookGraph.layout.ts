import { MarkerType, type Edge, type Node } from "@xyflow/react";
import dagre from "dagre";
import type { Playbook } from "@/lib/playbook";
import { diffPlaybooks, edgeId, type EdgeDiff } from "@/lib/playbook-diff";
import { NODE_WIDTH, NODE_HEIGHT, type PlaybookNodeData } from "./PlaybookGraph.nodes";
import type { ConditionEdgeData } from "./PlaybookGraph.edges";

// Edge stroke palette by diff status. Dangling targets keep their
// existing red-900 dashed look (distinct from "removed" red-700) so
// the operator can tell "this points nowhere in the proposal" apart
// from "this branch was dropped".
const EDGE_STROKE: Record<EdgeDiff, string> = {
  added: "#10b981", // emerald-500
  retargeted: "#f59e0b", // amber-500
  removed: "#b91c1c", // red-700
  unchanged: "#52525b", // zinc-600
};

// buildGraph converts the playbook into xyflow nodes + edges and runs
// dagre top-to-bottom layout. When basePlaybook is supplied the graph
// renders the union of both playbooks with diff status tagged on each
// node/edge. When omitted (existing call sites) the diff status is null
// everywhere and the output is identical to the original implementation.
export function buildGraph(
  playbook: Playbook,
  selectedId: string | null,
  onOpenPlaybook: ((id: string) => void) | undefined,
  basePlaybook?: Playbook,
): { nodes: Node[]; edges: Edge[] } {
  const diff = diffPlaybooks(playbook, basePlaybook);
  const hasBase = basePlaybook !== undefined;

  const g = new dagre.graphlib.Graph();
  g.setDefaultEdgeLabel(() => ({}));
  // Generous spacing: branch labels are wrappable HTML capped at 220px
  // and can grow several lines — give dagre enough vertical breathing
  // room (ranksep) and horizontal separation (nodesep) so labels rarely
  // overlap their neighbours.
  g.setGraph({ rankdir: "TB", nodesep: 80, ranksep: 140, marginx: 24, marginy: 24 });

  // Union of node ids — proposed wins for content on
  // unchanged/modified/added; base wins on removed (so the ghost
  // still shows the original description on hover).
  const baseNodes = basePlaybook?.nodes ?? {};
  const propNodes = playbook.nodes;
  const allIds = Array.from(
    new Set([...Object.keys(baseNodes), ...Object.keys(propNodes)]),
  );

  for (const id of allIds) {
    const status = diff.nodes.get(id) ?? null;
    const body = status === "removed" ? baseNodes[id] : propNodes[id] ?? baseNodes[id];
    const handoffCount = body.handoff?.length ?? 0;
    const hasDelegate = !!body.delegate_to;
    // Each row of chips below the description adds ~18px. Handoff and
    // delegate render in separate rows so they stack — both contribute.
    const extra = (handoffCount > 0 ? 18 : 0) + (hasDelegate ? 18 : 0);
    g.setNode(id, { width: NODE_WIDTH, height: NODE_HEIGHT + extra });
  }

  // Edge union — for each (source, index) slot, render exactly one
  // edge per side that owns it (proposed for unchanged/retargeted/added,
  // base for removed). This mirrors the diffPlaybooks edge map.
  const edges: Edge[] = [];
  const buildSlot = (
    source: string,
    index: number,
    branch: { condition: string; goto: string },
    status: EdgeDiff,
  ) => {
    const id = edgeId(source, index, branch.goto);
    // We treat a missing target *in the proposed playbook* as
    // dangling. For removed edges, "dangling" is computed against the
    // base playbook — its source/target both come from base.
    const targetMap = status === "removed" ? baseNodes : propNodes;
    const dangling = !targetMap[branch.goto];
    if (!dangling) {
      g.setEdge(source, branch.goto);
    }
    const baseStroke = dangling
      ? "#7f1d1d"
      : hasBase && status !== "unchanged"
        ? EDGE_STROKE[status]
        : EDGE_STROKE.unchanged;
    const dashed = dangling || (hasBase && status === "removed");
    const reducedOpacity = hasBase && status === "removed" ? 0.6 : 1;
    edges.push({
      id,
      source,
      target: dangling ? source : branch.goto,
      type: "condition",
      data: {
        condition: branch.condition,
        dangling,
        diffStatus: hasBase ? status : null,
      } satisfies ConditionEdgeData,
      style: {
        stroke: baseStroke,
        strokeDasharray: dashed ? "4 3" : undefined,
        opacity: reducedOpacity,
      },
      markerEnd: {
        type: MarkerType.ArrowClosed,
        color: baseStroke,
      },
    });
  };

  for (const src of allIds) {
    const baseNexts = baseNodes[src]?.next ?? [];
    const propNexts = propNodes[src]?.next ?? [];
    const slotCount = Math.max(baseNexts.length, propNexts.length);
    for (let i = 0; i < slotCount; i++) {
      const b = baseNexts[i];
      const p = propNexts[i];
      // Pick which side renders this slot:
      //   - present in proposed → render proposed (added/retargeted/unchanged)
      //   - only in base → render base (removed)
      // The diff helper's edges map is already keyed by the same edge id
      // we'd compute here, so we look up the status rather than re-deriving
      // it. This keeps the slot-identity rules (source + index, with key
      // taking proposed-or-base goto correctly) in one place.
      const branch = p ?? b;
      if (!branch) continue;
      const id = edgeId(src, i, branch.goto);
      const status: EdgeDiff = hasBase
        ? diff.edges.get(id) ?? "unchanged"
        : "unchanged";
      buildSlot(src, i, branch, status);
    }
  }

  dagre.layout(g);

  const nodes: Node[] = allIds.map((id) => {
    const status = diff.nodes.get(id) ?? null;
    const body = status === "removed" ? baseNodes[id] : propNodes[id] ?? baseNodes[id];
    const layout = g.node(id);
    const data: PlaybookNodeData = {
      label: id,
      description: body.description ?? "",
      // Entry/terminal reflect the *proposed* state for surviving
      // nodes. Removed nodes carry their base entry/terminal status —
      // useful for the rare case where the operator removed an
      // entrypoint (then the proposed entrypoint should also flag).
      isEntry:
        status === "removed"
          ? id === basePlaybook?.entrypoint
          : id === playbook.entrypoint,
      isTerminal: !!body.terminal_advice,
      isSelected: id === selectedId,
      handoff: body.handoff ?? [],
      delegateTo: body.delegate_to,
      onOpenPlaybook,
      diffStatus: hasBase ? status : null,
      modifiedFields: diff.modifiedFields.get(id) ?? [],
      wasEntry:
        hasBase &&
        diff.entrypointChanged &&
        id === basePlaybook?.entrypoint &&
        id !== playbook.entrypoint,
    };
    return {
      id,
      type: "playbook",
      data: data as unknown as Record<string, unknown>,
      position: {
        x: (layout?.x ?? 0) - NODE_WIDTH / 2,
        y: (layout?.y ?? 0) - NODE_HEIGHT / 2,
      },
    };
  });

  return { nodes, edges };
}
