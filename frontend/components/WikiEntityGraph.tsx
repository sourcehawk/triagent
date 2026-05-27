"use client";

// WikiEntityGraph renders the entity graph declared by a wiki
// incident page (the central node) and its referenced entities (the
// spokes). When given a `baseMd`, status overlays on each spoke
// surface added / removed / modified entities — the visual analogue
// of WikiProposalCard's text diff tab.
//
// Layout: dagre top-to-bottom with the wiki page as the root rank
// and entities arrayed below. Mirrors PlaybookGraph's palette and
// chip language so operators don't need to learn a second diff
// vocabulary.

import { useEffect, useMemo } from "react";
import {
  BaseEdge,
  Background,
  Controls,
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
import {
  diffWikiEntities,
  presentStatuses,
  type WikiEntityDiff,
  type WikiEntityDiffEntry,
  type WikiEntityStatus,
  type WikiEntityType,
} from "@/lib/wiki-entity-diff";
import { useClickToFocus } from "@/lib/use-click-to-focus";

type Props = {
  // Markdown of the proposed wiki page (after change applied).
  newMd: string;
  // Markdown of the current wiki page (before change). Omit / pass
  // empty for brand-new wikis — every entity becomes "added".
  baseMd?: string;
  // Display label for the central node. Defaults to "wiki page".
  slug?: string;
};

const NODE_WIDTH = 200;
const NODE_HEIGHT = 44;
const WIKI_NODE_WIDTH = 240;
const WIKI_NODE_HEIGHT = 56;

// Edge palette by status. Same hex values as PlaybookGraph so the
// two diff views share a vocabulary.
const EDGE_STROKE: Record<WikiEntityStatus, string> = {
  added: "#10b981", // emerald-500
  modified: "#f59e0b", // amber-500
  removed: "#b91c1c", // red-700
  unchanged: "#52525b", // zinc-600
};

// Entity-type accent for the spoke nodes. Kept distinct from the
// diff palette: type colour stays on the chip; the ring carries the
// diff status. The diff status takes visual precedence so a
// removed-service entity reads as red first, sky-blue second.
const TYPE_TONE: Record<WikiEntityType, { chip: string; label: string }> = {
  service: {
    chip: "bg-sky-900/60 text-sky-200",
    label: "service",
  },
  error: {
    chip: "bg-rose-900/60 text-rose-200",
    label: "error",
  },
  symptom: {
    chip: "bg-violet-900/60 text-violet-200",
    label: "symptom",
  },
  component: {
    chip: "bg-zinc-800 text-zinc-300",
    label: "component",
  },
};

export function WikiEntityGraph({ newMd, baseMd, slug }: Props) {
  return (
    <ReactFlowProvider>
      <WikiEntityGraphInner
        newMd={newMd}
        baseMd={baseMd}
        slug={slug}
      />
    </ReactFlowProvider>
  );
}

function WikiEntityGraphInner({ newMd, baseMd, slug }: Props) {
  const diff = useMemo(() => diffWikiEntities(newMd, baseMd), [newMd, baseMd]);
  const hasBase = !!baseMd && baseMd.length > 0;

  const built = useMemo(
    () => buildGraph(diff, slug ?? "wiki page", hasBase),
    [diff, slug, hasBase],
  );

  const [nodes, setNodes, onNodesChange] = useNodesState(built.nodes);
  const [edges, setEdges, onEdgesChange] = useEdgesState(built.edges);
  const { ref: focusRef, focused, focus } = useClickToFocus<HTMLDivElement>();

  useEffect(() => {
    setNodes(built.nodes);
    setEdges(built.edges);
  }, [built.nodes, built.edges, setNodes, setEdges]);

  const present = useMemo(() => presentStatuses(diff), [diff]);
  const entityCount = diff.entities.size;

  return (
    <div
      ref={focusRef}
      onPointerDown={focus}
      className="relative h-[60vh] w-full rounded border border-zinc-800 bg-zinc-950/60"
    >
      {hasBase && <DiffLegend present={present} />}
      {entityCount === 0 && (
        <div className="absolute inset-0 flex items-center justify-center text-xs text-zinc-500">
          no entity references in this wiki page
        </div>
      )}
      <ReactFlow
        nodes={nodes}
        edges={edges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        fitView
        fitViewOptions={{ padding: 0.2 }}
        minZoom={0.2}
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

// ── Custom node types ────────────────────────────────────────────────

type WikiCentralNodeData = {
  label: string;
  entityCount: number;
};

type EntityNodeData = {
  entry: WikiEntityDiffEntry;
  hasBase: boolean;
};

const nodeTypes = {
  wiki: WikiCentralNodeView,
  entity: EntityNodeView,
};

function WikiCentralNodeView({
  data,
}: NodeProps<Node<WikiCentralNodeData>>) {
  return (
    <div
      className="rounded border border-emerald-700/60 bg-emerald-950/40 px-3 py-2 text-left shadow-md"
      style={{ width: WIKI_NODE_WIDTH, minHeight: WIKI_NODE_HEIGHT }}
    >
      <div className="text-[10px] uppercase tracking-wide text-emerald-300/80">
        wiki page
      </div>
      <div className="truncate font-mono text-xs text-zinc-100">
        {data.label}
      </div>
      <div className="mt-0.5 text-[10px] text-zinc-500">
        {data.entityCount} entity reference{data.entityCount === 1 ? "" : "s"}
      </div>
      <Handle
        type="source"
        position={Position.Bottom}
        className="!bg-zinc-600"
      />
    </div>
  );
}

function EntityNodeView({ data }: NodeProps<Node<EntityNodeData>>) {
  const { entry, hasBase } = data;
  const ring = diffRingClass(entry.status, hasBase);
  const removed = entry.status === "removed";
  const bodyOpacity = removed ? "opacity-60" : "";
  const tone = TYPE_TONE[entry.entity.type];
  // Modified entities surface their changed dimensions in a tooltip
  // so we don't burn vertical layout space on every node.
  const modifiedTooltip = useMemo(() => {
    if (entry.status !== "modified") return undefined;
    const bits: string[] = [];
    if (entry.previousType) {
      bits.push(`type: ${entry.previousType} → ${entry.entity.type}`);
    }
    if (entry.previousSources) {
      bits.push(
        `sources: ${entry.previousSources.join("+")} → ${entry.entity.sources.join("+")}`,
      );
    }
    return bits.join(" · ");
  }, [entry]);

  return (
    <div
      className={
        "group relative rounded border border-zinc-700 bg-zinc-900 px-2.5 py-1.5 text-left shadow-md " +
        ring +
        " " +
        bodyOpacity
      }
      style={{ width: NODE_WIDTH, minHeight: NODE_HEIGHT }}
      title={modifiedTooltip}
    >
      <Handle type="target" position={Position.Top} className="!bg-zinc-600" />
      <div className="flex items-center justify-between gap-1">
        <span
          className={
            "truncate font-mono text-xs text-zinc-100 " +
            (removed ? "line-through decoration-red-400/60" : "")
          }
        >
          {entry.name}
        </span>
        <DiffChip status={entry.status} hasBase={hasBase} />
      </div>
      <div className="mt-1 flex items-center gap-1">
        <span
          className={`rounded-full px-1.5 py-0.5 text-[10px] font-medium ${tone.chip}`}
        >
          {tone.label}
        </span>
        <span className="text-[10px] text-zinc-500">
          {entry.entity.sources.join("+")}
        </span>
      </div>
    </div>
  );
}

function diffRingClass(
  status: WikiEntityStatus,
  hasBase: boolean,
): string {
  if (!hasBase) return "";
  switch (status) {
    case "added":
      return "ring-2 ring-emerald-500/70";
    case "modified":
      return "ring-2 ring-amber-500/70";
    case "removed":
      return "outline outline-2 outline-dashed outline-red-700/70 outline-offset-[-2px]";
    default:
      return "";
  }
}

function DiffChip({
  status,
  hasBase,
}: {
  status: WikiEntityStatus;
  hasBase: boolean;
}) {
  if (!hasBase || status === "unchanged") return null;
  const tone =
    status === "added"
      ? "bg-emerald-900/60 text-emerald-200"
      : status === "modified"
        ? "bg-amber-900/60 text-amber-200"
        : "bg-red-900/60 text-red-200";
  return (
    <span
      className={`rounded-full px-1.5 py-0.5 text-[10px] font-medium ${tone}`}
    >
      {status}
    </span>
  );
}

function DiffLegend({ present }: { present: Set<WikiEntityStatus> }) {
  const items: { label: string; status: WikiEntityStatus; tone: string }[] = [
    { label: "added", status: "added", tone: "bg-emerald-500" },
    { label: "modified", status: "modified", tone: "bg-amber-500" },
    { label: "removed", status: "removed", tone: "bg-red-700" },
    { label: "unchanged", status: "unchanged", tone: "bg-zinc-500" },
  ];
  return (
    <div className="pointer-events-none absolute left-2 top-2 z-10 flex flex-wrap items-center gap-2 rounded border border-zinc-800 bg-zinc-950/80 px-2 py-1 text-xs text-zinc-300 backdrop-blur-sm">
      {items.map((it) => (
        <span
          key={it.label}
          className={
            "flex items-center gap-1 " +
            (present.has(it.status) ? "" : "opacity-40")
          }
        >
          <span className={`inline-block h-2 w-2 rounded-full ${it.tone}`} />
          {it.label}
        </span>
      ))}
    </div>
  );
}

// ── Custom edge: thin curve, status-coloured ─────────────────────────

type SpokeEdgeData = {
  status: WikiEntityStatus;
  hasBase: boolean;
};

const edgeTypes = {
  spoke: SpokeEdge,
};

function SpokeEdge(props: EdgeProps<Edge<SpokeEdgeData>>) {
  const { id, sourceX, sourceY, targetX, targetY, style, markerEnd } = props;
  const [path] = getBezierPath({
    sourceX,
    sourceY,
    sourcePosition: Position.Bottom,
    targetX,
    targetY,
    targetPosition: Position.Top,
  });
  return <BaseEdge id={id} path={path} style={style} markerEnd={markerEnd} />;
}

// ── Layout ───────────────────────────────────────────────────────────

const WIKI_NODE_ID = "__wiki__";

function buildGraph(
  diff: WikiEntityDiff,
  wikiLabel: string,
  hasBase: boolean,
): { nodes: Node[]; edges: Edge[] } {
  const g = new dagre.graphlib.Graph();
  g.setDefaultEdgeLabel(() => ({}));
  g.setGraph({
    rankdir: "TB",
    nodesep: 30,
    ranksep: 80,
    marginx: 24,
    marginy: 24,
  });

  // Sort entities for stable rendering: by type, then by name. The
  // dagre layout doesn't itself preserve insertion order, but giving
  // it nodes in a deterministic sequence keeps things steady across
  // re-renders triggered by parent re-renders.
  const entries = Array.from(diff.entities.values()).sort((a, b) => {
    const typeOrder: Record<WikiEntityType, number> = {
      service: 0,
      error: 1,
      symptom: 2,
      component: 3,
    };
    const t = typeOrder[a.entity.type] - typeOrder[b.entity.type];
    if (t !== 0) return t;
    return a.name.localeCompare(b.name);
  });

  g.setNode(WIKI_NODE_ID, {
    width: WIKI_NODE_WIDTH,
    height: WIKI_NODE_HEIGHT,
  });
  for (const entry of entries) {
    g.setNode(entry.name, { width: NODE_WIDTH, height: NODE_HEIGHT });
    g.setEdge(WIKI_NODE_ID, entry.name);
  }

  dagre.layout(g);

  const nodes: Node[] = [];
  const wikiLayout = g.node(WIKI_NODE_ID);
  nodes.push({
    id: WIKI_NODE_ID,
    type: "wiki",
    data: {
      label: wikiLabel,
      entityCount: entries.length,
    } as unknown as Record<string, unknown>,
    position: {
      x: (wikiLayout?.x ?? 0) - WIKI_NODE_WIDTH / 2,
      y: (wikiLayout?.y ?? 0) - WIKI_NODE_HEIGHT / 2,
    },
    draggable: false,
    selectable: false,
  });

  for (const entry of entries) {
    const layout = g.node(entry.name);
    nodes.push({
      id: entry.name,
      type: "entity",
      data: { entry, hasBase } as unknown as Record<string, unknown>,
      position: {
        x: (layout?.x ?? 0) - NODE_WIDTH / 2,
        y: (layout?.y ?? 0) - NODE_HEIGHT / 2,
      },
    });
  }

  const edges: Edge[] = entries.map((entry) => {
    const status = entry.status;
    const stroke = hasBase ? EDGE_STROKE[status] : EDGE_STROKE.unchanged;
    const dashed = hasBase && status === "removed";
    const opacity = hasBase && status === "removed" ? 0.6 : 1;
    return {
      id: `${WIKI_NODE_ID}__${entry.name}`,
      source: WIKI_NODE_ID,
      target: entry.name,
      type: "spoke",
      data: { status, hasBase } satisfies SpokeEdgeData,
      style: {
        stroke,
        strokeDasharray: dashed ? "4 3" : undefined,
        opacity,
      },
      markerEnd: {
        type: MarkerType.ArrowClosed,
        color: stroke,
      },
    };
  });

  return { nodes, edges };
}
