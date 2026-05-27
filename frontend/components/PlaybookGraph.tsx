"use client";

import { useEffect, useMemo } from "react";
import {
  BaseEdge,
  Background,
  Controls,
  EdgeLabelRenderer,
  getBezierPath,
  MarkerType,
  ReactFlow,
  ReactFlowProvider,
  type Edge,
  type EdgeProps,
  type Node,
  type NodeProps,
  Handle,
  Position,
  useNodesState,
  useEdgesState,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import dagre from "dagre";
import type { Playbook } from "@/lib/playbook";
import { diffPlaybooks, edgeId, type NodeDiff, type EdgeDiff } from "@/lib/playbook-diff";
import { useClickToFocus } from "@/lib/use-click-to-focus";

type Props = {
  playbook: Playbook;
  selectedId: string | null;
  onSelect: (id: string) => void;
  // Click handler for handoff chips on terminal nodes — navigates the
  // editor to that playbook. Optional so the graph can still be used
  // outside the editor (no-op when not provided).
  onOpenPlaybook?: (id: string) => void;
  // When provided, the graph renders the union of base + playbook
  // with diff status overlays on nodes and edges. When omitted the
  // graph renders just `playbook` with no diff styling — existing
  // call sites unchanged.
  basePlaybook?: Playbook;
};

// Stable layout node sizes — dagre lays things out using these as bounding
// boxes; the actual node body honours them via fixed-width CSS so the
// final render aligns with the layout pass.
//
// Compact height (header only): the description expands in place on hover
// via a grid-rows animation, so it doesn't need layout space.
const NODE_WIDTH = 220;
const NODE_HEIGHT = 38;

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

// PlaybookGraph wraps the playbook → xyflow conversion and the dagre
// auto-layout pass. Every change to `playbook` re-runs the layout from
// scratch — we don't try to preserve manual node positions because the
// playbook YAML has no place to store them.
export function PlaybookGraph({
  playbook,
  selectedId,
  onSelect,
  onOpenPlaybook,
  basePlaybook,
}: Props) {
  return (
    <ReactFlowProvider>
      <PlaybookGraphInner
        playbook={playbook}
        selectedId={selectedId}
        onSelect={onSelect}
        onOpenPlaybook={onOpenPlaybook}
        basePlaybook={basePlaybook}
      />
    </ReactFlowProvider>
  );
}

function PlaybookGraphInner({
  playbook,
  selectedId,
  onSelect,
  onOpenPlaybook,
  basePlaybook,
}: Props) {
  const built = useMemo(
    () => buildGraph(playbook, selectedId, onOpenPlaybook, basePlaybook),
    [playbook, selectedId, onOpenPlaybook, basePlaybook],
  );

  const [nodes, setNodes, onNodesChange] = useNodesState(built.nodes);
  const [edges, setEdges, onEdgesChange] = useEdgesState(built.edges);
  const { ref: focusRef, focused, focus } = useClickToFocus<HTMLDivElement>();

  // Re-sync when the source playbook changes (or selection — for the
  // highlight). Drag-induced layout changes stay local until the next
  // playbook edit, which is the right semantic for an in-flight session.
  useEffect(() => {
    setNodes(built.nodes);
    setEdges(built.edges);
  }, [built.nodes, built.edges, setNodes, setEdges]);

  const showLegend = basePlaybook !== undefined;
  const presentNodeStatuses = useMemo(() => {
    const s = new Set<NodeDiff>();
    for (const n of built.nodes) {
      const d = (n.data as unknown as PlaybookNodeData).diffStatus;
      if (d) s.add(d);
    }
    return s;
  }, [built.nodes]);
  const presentEdgeStatuses = useMemo(() => {
    const s = new Set<EdgeDiff>();
    for (const e of built.edges) {
      const d = (e.data as unknown as ConditionEdgeData).diffStatus;
      if (d) s.add(d);
    }
    return s;
  }, [built.edges]);

  return (
    <div
      ref={focusRef}
      onPointerDown={focus}
      className="relative h-full w-full rounded border border-zinc-800 bg-zinc-950/60"
    >
      {showLegend && (
        <DiffLegend
          presentNodeStatuses={presentNodeStatuses}
          presentEdgeStatuses={presentEdgeStatuses}
        />
      )}
      <ReactFlow
        nodes={nodes}
        edges={edges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onNodeClick={(_, n) => onSelect(n.id)}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        fitView
        fitViewOptions={{ padding: 0.2 }}
        minZoom={0.15}
        maxZoom={1.5}
        zoomOnScroll={focused}
        zoomOnPinch={focused}
        proOptions={{ hideAttribution: true }}
      >
        <Background color="#27272a" gap={16} />
        <Controls position="bottom-right" showInteractive={false} />
      </ReactFlow>
    </div>
  );
}

type PlaybookNodeData = {
  label: string;
  description: string;
  isEntry: boolean;
  isTerminal: boolean;
  isSelected: boolean;
  handoff: string[];
  // delegate_to target on this node — undefined for non-delegate nodes.
  // Surfaces in the rendered node as a sky-blue chip + clickable target
  // button, parallel to the terminal/handoff treatment.
  delegateTo: string | undefined;
  // Click handler for individual handoff chips. Stored on node data so
  // the custom node component can call it without a context — the
  // graph container threads it through buildGraph below.
  onOpenPlaybook?: (id: string) => void;
  // Diff overlay — null when no base provided. The renderer uses
  // these to apply ring/chip/desat styling on top of the existing
  // entry/terminal/selected treatment.
  diffStatus: NodeDiff | null;
  // Names of fields that changed (only when diffStatus === "modified").
  // Drives the modified-fields list appended to the description popover.
  modifiedFields: string[];
  // True when this node was the *base* entrypoint and the proposed
  // entrypoint is different. Drives a "was entry" amber chip.
  wasEntry: boolean;
};

const nodeTypes = {
  playbook: PlaybookNodeView,
};

function PlaybookNodeView({ data }: NodeProps<Node<PlaybookNodeData>>) {
  // Ring precedence: selected > diff status > entry > terminal > delegate.
  // Removed nodes still respect a sky selection ring so the operator
  // can click into them. Delegate uses sky-700 to parallel the handoff
  // chip palette and signal "this node hops into a sub-flow before
  // continuing"; sky-400 stays reserved for selection so the two read
  // distinctly.
  const isDelegate = !!data.delegateTo;
  const diffRing = diffRingClass(data.diffStatus);
  const ring = data.isSelected
    ? "ring-2 ring-sky-400/80"
    : diffRing
      ? diffRing
      : data.isEntry
        ? "ring-2 ring-emerald-500/40"
        : data.isTerminal
          ? "ring-2 ring-amber-600/40"
          : isDelegate
            ? "ring-2 ring-sky-700/50"
            : "";

  // "Removed" nodes desaturate the body and strikethrough the label
  // to read as ghosts in the layout. Sky-selection ring still
  // overrides desat opacity at the parent level so a click reads.
  const removed = data.diffStatus === "removed";
  const bodyOpacity = removed ? "opacity-60" : "";

  return (
    <div
      className={
        "group relative rounded border border-zinc-700 bg-zinc-900 px-2.5 py-1.5 text-left shadow-md hover:z-[1000] " +
        ring +
        " " +
        bodyOpacity
      }
      style={{ width: NODE_WIDTH, minHeight: NODE_HEIGHT }}
    >
      <Handle type="target" position={Position.Top} className="!bg-zinc-600" />
      <div className="flex flex-wrap items-center justify-between gap-1">
        <span
          className={
            "truncate font-mono text-xs text-zinc-100 " +
            (removed ? "line-through decoration-red-400/60" : "")
          }
        >
          {data.label}
        </span>
        <div className="flex items-center gap-1">
          {data.isEntry && (
            <span className="rounded-full bg-emerald-900/60 px-1.5 py-0.5 text-xs font-medium text-emerald-200">
              entry
            </span>
          )}
          {data.wasEntry && (
            <span
              className="rounded-full bg-amber-900/60 px-1.5 py-0.5 text-xs font-medium text-amber-200"
              title="this was the entrypoint in the base playbook"
            >
              was entry
            </span>
          )}
          {data.isTerminal && (
            <span className="rounded-full bg-amber-900/60 px-1.5 py-0.5 text-xs font-medium text-amber-200">
              terminal
            </span>
          )}
          {isDelegate && (
            <span
              className="rounded-full bg-sky-900/60 px-1.5 py-0.5 text-xs font-medium text-sky-200"
              title={`sub-flow: walks ${data.delegateTo} before resuming`}
            >
              delegate
            </span>
          )}
          <DiffChip status={data.diffStatus} />
        </div>
      </div>
      {data.handoff.length > 0 && (
        <div className="mt-1 flex flex-wrap gap-1">
          {data.handoff.map((id) => (
            <button
              key={id}
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                data.onOpenPlaybook?.(id);
              }}
              title={`open ${id}`}
              className="inline-flex max-w-full min-w-0 items-center gap-1 rounded border border-sky-900/60 bg-sky-950/40 px-1.5 py-0.5 font-mono text-xs text-sky-200 transition hover:border-sky-600 hover:bg-sky-900/40"
            >
              <span aria-hidden className="shrink-0">→</span>
              <span className="min-w-0 truncate">{id}</span>
            </button>
          ))}
        </div>
      )}
      {data.delegateTo && (
        <div className="mt-1 flex flex-wrap gap-1">
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              data.onOpenPlaybook?.(data.delegateTo!);
            }}
            title={`open ${data.delegateTo}`}
            className="inline-flex max-w-full min-w-0 items-center gap-1 rounded border border-sky-900/60 bg-sky-950/40 px-1.5 py-0.5 font-mono text-xs text-sky-200 transition hover:border-sky-600 hover:bg-sky-900/40"
          >
            <span aria-hidden className="shrink-0">↳</span>
            <span className="min-w-0 truncate">{data.delegateTo}</span>
          </button>
        </div>
      )}
      {(data.description || data.modifiedFields.length > 0) && (
        // grid-cols-[minmax(0,1fr)] pins the implicit column to the node's
        // 220px body — without it, a long unbreakable token in the
        // description (URL, identifier) would expand the auto column past
        // the node and visibly stick out to the right of the screen.
        <div className="grid grid-cols-[minmax(0,1fr)] grid-rows-[0fr] transition-[grid-template-rows] duration-200 ease-out group-hover:grid-rows-[1fr]">
          <div className="min-w-0 overflow-hidden">
            <div className="mt-1.5 max-h-72 overflow-y-auto whitespace-pre-wrap break-words text-xs leading-relaxed text-zinc-300">
              {data.description}
              {data.modifiedFields.length > 0 && (
                <div className="mt-1.5 border-t border-zinc-800 pt-1.5 text-xs text-amber-300/80">
                  changed: {data.modifiedFields.join(", ")}
                </div>
              )}
            </div>
          </div>
        </div>
      )}
      <Handle type="source" position={Position.Bottom} className="!bg-zinc-600" />
    </div>
  );
}

function diffRingClass(status: NodeDiff | null): string {
  switch (status) {
    case "added":
      return "ring-2 ring-emerald-500/70";
    case "modified":
      return "ring-2 ring-amber-500/70";
    case "removed":
      // Tailwind core has no dashed ring utility, so we use outline
      // (which supports `outline-dashed`) and pull it inside the
      // border with a negative offset so the ring sits on the box edge.
      return "outline outline-2 outline-dashed outline-red-700/70 outline-offset-[-2px]";
    default:
      return "";
  }
}

function DiffChip({ status }: { status: NodeDiff | null }) {
  if (!status || status === "unchanged") return null;
  const tone =
    status === "added"
      ? "bg-emerald-900/60 text-emerald-200"
      : status === "modified"
        ? "bg-amber-900/60 text-amber-200"
        : "bg-red-900/60 text-red-200";
  return (
    <span className={`rounded-full px-1.5 py-0.5 text-xs font-medium ${tone}`}>
      {status}
    </span>
  );
}

function DiffLegend({
  presentNodeStatuses,
  presentEdgeStatuses,
}: {
  presentNodeStatuses: Set<NodeDiff>;
  presentEdgeStatuses: Set<EdgeDiff>;
}) {
  // The legend's shape stays stable across diffs — statuses that
  // don't appear are dimmed so the operator can still see the key.
  const items: { label: string; status: NodeDiff | EdgeDiff; tone: string }[] = [
    { label: "added", status: "added", tone: "bg-emerald-500" },
    { label: "modified", status: "modified", tone: "bg-amber-500" },
    { label: "removed", status: "removed", tone: "bg-red-700" },
    { label: "retargeted", status: "retargeted", tone: "bg-amber-500" },
  ];
  return (
    <div className="pointer-events-none absolute left-2 top-2 z-10 flex flex-wrap items-center gap-2 rounded border border-zinc-800 bg-zinc-950/80 px-2 py-1 text-xs text-zinc-300 backdrop-blur-sm">
      {items.map((it) => {
        const present =
          presentNodeStatuses.has(it.status as NodeDiff) ||
          presentEdgeStatuses.has(it.status as EdgeDiff);
        return (
          <span
            key={it.label}
            className={"flex items-center gap-1 " + (present ? "" : "opacity-40")}
          >
            <span className={`inline-block h-2 w-2 rounded-full ${it.tone}`} />
            {it.label}
          </span>
        );
      })}
    </div>
  );
}

// ConditionEdgeData rides on each edge so the custom edge can render the
// full condition prose as wrapping HTML rather than a single SVG <text>.
type ConditionEdgeData = {
  condition: string;
  dangling: boolean;
  diffStatus: EdgeDiff | null;
};

const edgeTypes = {
  condition: ConditionEdge,
};

// ConditionEdge replaces xyflow's default SVG label (single line, no
// wrap) with an HTML label rendered via EdgeLabelRenderer. The label is
// width-capped, wraps at word boundaries, and exposes the full condition
// on hover via the browser's native title tooltip — long branch
// conditions (~80-200 chars in practice) become legible without zoom.
function ConditionEdge(props: EdgeProps<Edge<ConditionEdgeData>>) {
  const { id, sourceX, sourceY, targetX, targetY, style, markerEnd, data } =
    props;
  const [path, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    sourcePosition: Position.Bottom,
    targetX,
    targetY,
    targetPosition: Position.Top,
  });
  const tone = data?.dangling
    ? "border-red-900/70 bg-red-950/85 text-red-200"
    : data?.diffStatus === "added"
      ? "border-emerald-700/70 bg-emerald-950/85 text-emerald-200"
      : data?.diffStatus === "retargeted"
        ? "border-amber-700/70 bg-amber-950/85 text-amber-200"
        : data?.diffStatus === "removed"
          ? "border-red-800/70 bg-red-950/70 text-red-300/80"
          : "border-zinc-700/70 bg-zinc-950/90 text-zinc-300";
  return (
    <>
      <BaseEdge id={id} path={path} style={style} markerEnd={markerEnd} />
      {data?.condition && (
        <EdgeLabelRenderer>
          <div
            style={{
              position: "absolute",
              transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`,
              pointerEvents: "all",
            }}
            // Collapsed by default: a tiny single-line chip ~140px wide
            // showing the start of the condition. On hover the chip
            // expands to ~280px, allows wrapping, and lifts above its
            // neighbours. The big z-index pairs with the
            // .react-flow__edgelabel-renderer > *:hover rule in
            // globals.css so the portal wrapper also rises above peer
            // labels, not just our own div.
            className={
              "group max-w-[140px] cursor-default overflow-hidden rounded border px-1.5 py-0.5 text-xs leading-snug shadow-sm transition-all duration-150 " +
              "whitespace-nowrap text-ellipsis " +
              "hover:z-[1000] hover:max-w-[280px] hover:whitespace-pre-wrap hover:break-words hover:px-2 hover:py-1 " +
              tone
            }
          >
            {data.condition}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  );
}

// buildGraph converts the playbook into xyflow nodes + edges and runs
// dagre top-to-bottom layout. When basePlaybook is supplied the graph
// renders the union of both playbooks with diff status tagged on each
// node/edge. When omitted (existing call sites) the diff status is null
// everywhere and the output is identical to the original implementation.
function buildGraph(
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
