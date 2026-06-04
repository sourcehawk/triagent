"use client";

import { Handle, Position, type Node, type NodeProps } from "@xyflow/react";
import type { NodeDiff } from "@/lib/playbook-diff";

// Stable layout node sizes — dagre lays things out using these as bounding
// boxes; the actual node body honours them via fixed-width CSS so the
// final render aligns with the layout pass.
//
// Compact height (header only): the description expands in place on hover
// via a grid-rows animation, so it doesn't need layout space.
export const NODE_WIDTH = 220;
export const NODE_HEIGHT = 38;

export type PlaybookNodeData = {
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

export const nodeTypes = {
  playbook: PlaybookNodeView,
};

export function PlaybookNodeView({ data }: NodeProps<Node<PlaybookNodeData>>) {
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

export function diffRingClass(status: NodeDiff | null): string {
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

export function DiffChip({ status }: { status: NodeDiff | null }) {
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
