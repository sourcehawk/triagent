"use client";

import { useEffect, useMemo } from "react";
import {
  Background,
  Controls,
  ReactFlow,
  ReactFlowProvider,
  useNodesState,
  useEdgesState,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import type { Playbook } from "@/lib/playbook";
import { type NodeDiff, type EdgeDiff } from "@/lib/playbook-diff";
import { useClickToFocus } from "@/lib/use-click-to-focus";
import { nodeTypes, type PlaybookNodeData } from "./PlaybookGraph.nodes";
import { edgeTypes, type ConditionEdgeData } from "./PlaybookGraph.edges";
import { buildGraph } from "./PlaybookGraph.layout";

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
