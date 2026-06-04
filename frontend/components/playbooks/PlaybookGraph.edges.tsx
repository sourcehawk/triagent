"use client";

import {
  BaseEdge,
  EdgeLabelRenderer,
  getBezierPath,
  Position,
  type Edge,
  type EdgeProps,
} from "@xyflow/react";
import type { EdgeDiff } from "@/lib/playbook-diff";

// ConditionEdgeData rides on each edge so the custom edge can render the
// full condition prose as wrapping HTML rather than a single SVG <text>.
export type ConditionEdgeData = {
  condition: string;
  dangling: boolean;
  diffStatus: EdgeDiff | null;
};

export const edgeTypes = {
  condition: ConditionEdge,
};

// ConditionEdge replaces xyflow's default SVG label (single line, no
// wrap) with an HTML label rendered via EdgeLabelRenderer. The label is
// width-capped, wraps at word boundaries, and exposes the full condition
// on hover via the browser's native title tooltip — long branch
// conditions (~80-200 chars in practice) become legible without zoom.
export function ConditionEdge(props: EdgeProps<Edge<ConditionEdgeData>>) {
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
