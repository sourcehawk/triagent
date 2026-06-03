"use client";

import { Handle, Position, type NodeProps, type Node } from "@xyflow/react";
import {
  ChevronDownIcon,
  ChevronUpIcon,
  ExternalLinkIcon,
} from "@/components/shared/Icons";

// ── Shared sizes — must match the dagre layout hints in WikiGraph.tsx ────────

export const INCIDENT_NODE_WIDTH = 240;
export const INCIDENT_NODE_HEIGHT = 52;
export const ENTITY_NODE_WIDTH = 160;
export const ENTITY_NODE_HEIGHT = 36;

// ── Node data shapes ─────────────────────────────────────────────────────────

export type IncidentNodeData = {
  kind: "incident";
  incidentId: string;
  title: string;
  severity?: string;
  status?: string;
  isRoot: boolean;
  expanded: boolean;
  loading: boolean;
  // childCount is set after a successful expand: how many neighbours
  // were added to the graph (or already present + linked). 0 means
  // "expanded but nothing to show" — the node visibly flips to that
  // state so the operator knows clicking again won't help.
  childCount?: number;
  // selected: this node is the active selection in the editor's side
  // panel. Renders a sky-blue ring identical to the playbook editor's
  // selected-node treatment.
  selected?: boolean;
  // dirty: the operator has made unsaved edits to this node. Drives a
  // small amber dot in the top-right of the node card.
  dirty?: boolean;
  onExpand: (key: string) => void;
  onNavigate: (key: string) => void;
  // onSelect (when set) makes left-click select the node instead of
  // navigating away. The editor wires this; the read-only graph leaves
  // it unset and falls back to the navigate behaviour.
  onSelect?: (key: string) => void;
  // onCollapse (when set) is called from the chevron-up affordance on
  // an expanded non-root node. Children that became visible because
  // of this node's expansion are pruned; children also linked from
  // elsewhere stay.
  onCollapse?: (key: string) => void;
};

export type EntityNodeData = {
  kind: "entity";
  entityType: string;
  entityName: string;
  isRoot: boolean;
  expanded: boolean;
  loading: boolean;
  childCount?: number;
  selected?: boolean;
  dirty?: boolean;
  // prospectiveChildren is how many incidents would be *newly* added
  // by expanding this entity — total backlinks minus the incidents
  // already connected in the graph. Set when the entity counts have
  // been fetched. 0 means "no point expanding"; the chevron is
  // hidden in that case. undefined means "count not yet known"; the
  // chevron is shown without a number as a fallback.
  prospectiveChildren?: number;
  onExpand: (key: string) => void;
  onNavigate: (key: string) => void;
  onSelect?: (key: string) => void;
  onCollapse?: (key: string) => void;
};

// ── Incident node ────────────────────────────────────────────────────────────

export function IncidentNodeView({
  data,
}: NodeProps<Node<Record<string, unknown>>>) {
  const d = data as unknown as IncidentNodeData;
  const key = `incident:${d.incidentId}`;

  // selection takes priority over the root ring so the operator can
  // see which node the side panel is currently bound to.
  const ring = d.selected
    ? "ring-2 ring-sky-400"
    : d.isRoot
      ? "ring-2 ring-emerald-500"
      : "";
  // Dashed border on un-expanded nodes is a stronger visual cue than the
  // small "+" pill alone — the whole edge of the node says "more inside".
  const borderStyle = !d.expanded && !d.loading ? "border-dashed" : "border-solid";

  function handleClick() {
    if (d.loading) return;
    // When the editor wires onSelect, left-click selects the node.
    // Expand/navigate moves to the dedicated chevron + open-affordance
    // surfaces so a single click does the most-common thing (select).
    if (d.onSelect) {
      d.onSelect(key);
      return;
    }
    if (d.expanded) {
      d.onNavigate(key);
    } else {
      d.onExpand(key);
    }
  }

  return (
    <div
      className={
        "group relative cursor-pointer rounded border-2 border-emerald-900 bg-emerald-950/30 px-2.5 py-1.5 shadow-md " +
        "transition hover:border-emerald-600 hover:bg-emerald-950/60 " +
        borderStyle + " " +
        ring
      }
      style={{ width: INCIDENT_NODE_WIDTH, minHeight: INCIDENT_NODE_HEIGHT }}
      onClick={handleClick}
      title={
        d.expanded
          ? d.childCount === 0
            ? `${d.incidentId} — no further connections. Click to open detail page.`
            : `${d.incidentId} (${d.childCount ?? "expanded"}). Click to open detail page.`
          : `Click to expand ${d.incidentId}`
      }
    >
      <Handle type="target" position={Position.Top} className="!bg-emerald-800" />

      <div className="flex items-start justify-between gap-1.5">
        <div className="min-w-0 flex-1">
          <div className="font-mono text-[10px] leading-tight text-zinc-400">
            {d.incidentId}
          </div>
          <div className="mt-0.5 truncate text-xs font-medium leading-tight text-zinc-100">
            {d.title || d.incidentId}
          </div>
        </div>
        <div className="flex shrink-0 flex-col items-end gap-1">
          {d.dirty && (
            <span
              title="unsaved edits"
              aria-label="unsaved edits"
              className="h-1.5 w-1.5 rounded-full bg-amber-400 ring-1 ring-amber-200/40"
            />
          )}
          {/* When onSelect is set, left-click selects. The chevron
              still expands an unexpanded node; an open-affordance
              navigates. Clicks on these stop propagation so they don't
              bubble back to the node-card click handler. */}
          {!d.expanded && !d.loading && d.onSelect && (
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                d.onExpand(key);
              }}
              title={`expand ${d.incidentId}`}
              aria-label={`expand ${d.incidentId}`}
              className="inline-flex items-center justify-center rounded-full bg-emerald-800/80 px-1 py-0.5 text-emerald-100 transition hover:bg-emerald-700"
            >
              <ChevronDownIcon className="h-3 w-3" />
            </button>
          )}
          {d.expanded && !d.loading && !d.isRoot && d.onCollapse && (
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                d.onCollapse?.(key);
              }}
              title={`collapse ${d.incidentId}`}
              aria-label={`collapse ${d.incidentId}`}
              className="inline-flex items-center justify-center rounded-full bg-emerald-900/40 px-1 py-0.5 text-emerald-200/70 transition hover:bg-emerald-800/60 hover:text-emerald-100"
            >
              <ChevronUpIcon className="h-3 w-3" />
            </button>
          )}
          {!d.onSelect && (
            <ExpandBadge
              expanded={d.expanded}
              loading={d.loading}
              childCount={d.childCount}
              tone="emerald"
            />
          )}
          {d.onSelect && (
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                d.onNavigate(key);
              }}
              title="open in dedicated page"
              aria-label="open in dedicated page"
              className="inline-flex items-center justify-center rounded text-zinc-500 transition hover:text-zinc-200"
            >
              <ExternalLinkIcon className="h-3 w-3" />
            </button>
          )}
        </div>
      </div>

      <Handle type="source" position={Position.Bottom} className="!bg-emerald-800" />
    </div>
  );
}

// ── Entity node ──────────────────────────────────────────────────────────────

export function EntityNodeView({
  data,
}: NodeProps<Node<Record<string, unknown>>>) {
  const d = data as unknown as EntityNodeData;
  const key = `entity:${d.entityType}:${d.entityName}`;

  const { bg, border, text } = entityTypeColors(d.entityType);
  // Selection ring matches the incident-node treatment so the operator
  // can spot the active node at a glance.
  const ring = d.selected
    ? "ring-2 ring-sky-400"
    : d.isRoot
      ? "ring-2 ring-sky-400"
      : "";
  const borderStyle = !d.expanded && !d.loading ? "border-dashed" : "border-solid";

  function handleClick() {
    if (d.loading) return;
    if (d.onSelect) {
      d.onSelect(key);
      return;
    }
    if (d.expanded) {
      d.onNavigate(key);
    } else {
      d.onExpand(key);
    }
  }

  return (
    <div
      className={
        `relative cursor-pointer rounded-full border-2 ${bg} ${border} px-3 py-1.5 shadow-md ` +
        `transition hover:opacity-95 hover:shadow-lg ` +
        borderStyle + " " +
        ring
      }
      style={{ width: ENTITY_NODE_WIDTH, minHeight: ENTITY_NODE_HEIGHT }}
      onClick={handleClick}
      title={
        d.expanded
          ? d.childCount === 0
            ? `${d.entityName} — no further connections. Click to open detail page.`
            : `${d.entityName} (${d.childCount ?? "expanded"}). Click to open detail page.`
          : `Click to expand ${d.entityName}`
      }
    >
      <Handle type="target" position={Position.Top} className="!bg-zinc-600" />

      <div className="flex items-center justify-between gap-1">
        <div className="min-w-0 flex-1">
          <div className={`truncate font-mono text-[10px] leading-tight ${text}`}>
            {d.entityName}
          </div>
          <div className="text-[9px] font-semibold uppercase leading-tight text-zinc-500">
            {d.entityType}
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          {d.dirty && (
            <span
              title="unsaved edits"
              aria-label="unsaved edits"
              className="h-1.5 w-1.5 rounded-full bg-amber-400 ring-1 ring-amber-200/40"
            />
          )}
          {!d.expanded &&
            !d.loading &&
            d.onSelect &&
            d.prospectiveChildren !== 0 && (
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  d.onExpand(key);
                }}
                title={
                  d.prospectiveChildren !== undefined
                    ? `expand to reveal ${d.prospectiveChildren} other incident${d.prospectiveChildren !== 1 ? "s" : ""} referencing ${d.entityName}`
                    : `expand ${d.entityName}`
                }
                aria-label={`expand ${d.entityName}`}
                className="inline-flex items-center gap-0.5 rounded-full bg-zinc-700/80 px-1 py-0.5 text-zinc-200 transition hover:bg-zinc-600"
              >
                <ChevronDownIcon className="h-3 w-3" />
                {d.prospectiveChildren !== undefined &&
                  d.prospectiveChildren > 0 && (
                    <span className="pr-0.5 text-[9px] font-semibold tabular-nums">
                      {d.prospectiveChildren}
                    </span>
                  )}
              </button>
            )}
          {d.expanded && !d.loading && !d.isRoot && d.onCollapse && (
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                d.onCollapse?.(key);
              }}
              title={`collapse ${d.entityName}`}
              aria-label={`collapse ${d.entityName}`}
              className="inline-flex items-center justify-center rounded-full bg-zinc-800/60 px-1 py-0.5 text-zinc-300/80 transition hover:bg-zinc-700 hover:text-zinc-100"
            >
              <ChevronUpIcon className="h-3 w-3" />
            </button>
          )}
          {!d.onSelect && (
            <ExpandBadge
              expanded={d.expanded}
              loading={d.loading}
              childCount={d.childCount}
              tone="entity"
              entityText={text}
            />
          )}
          {d.onSelect && (
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                d.onNavigate(key);
              }}
              title="open in dedicated page"
              aria-label="open in dedicated page"
              className="inline-flex items-center justify-center rounded text-zinc-500 transition hover:text-zinc-200"
            >
              <ExternalLinkIcon className="h-3 w-3" />
            </button>
          )}
        </div>
      </div>

      <Handle type="source" position={Position.Bottom} className="!bg-zinc-600" />
    </div>
  );
}

// ── Expand / count badge (shared by both incident and entity nodes) ─────────

type ExpandBadgeProps = {
  expanded: boolean;
  loading: boolean;
  childCount?: number;
  // tone="emerald" picks the incident-node colours; tone="entity" picks
  // a tone keyed off the supplied entityText (sky/rose/amber/zinc class).
  tone: "emerald" | "entity";
  entityText?: string;
};

function ExpandBadge({ expanded, loading, childCount, tone, entityText }: ExpandBadgeProps) {
  if (loading) {
    return (
      <span className="h-3 w-3 animate-spin rounded-full border border-zinc-400 border-t-transparent" />
    );
  }
  if (!expanded) {
    // Pre-expansion: show a chevron-down ("▾") + a "click to expand" label
    // on hover. The chevron + dashed node border together signal
    // "there's more inside, click to reveal" much louder than the prior
    // bare "+" pill.
    const cls =
      tone === "emerald"
        ? "bg-emerald-700 text-emerald-100"
        : "bg-zinc-700/80 text-zinc-200";
    return (
      <span
        className={`inline-flex items-center justify-center rounded-full ${cls} px-1 py-0.5`}
      >
        <ChevronDownIcon className="h-3 w-3" />
      </span>
    );
  }
  // Post-expansion: show the child count. 0 means "fully explored, no
  // further branches" — render a dimmer "•" so the operator sees that
  // clicking again won't help. >0 shows the number itself in the node's
  // accent colour.
  if (childCount === 0) {
    return (
      <span
        className="rounded-full bg-zinc-800 px-1.5 py-0.5 text-[9px] font-medium text-zinc-500"
        aria-label="no further connections"
      >
        ·
      </span>
    );
  }
  if (childCount && childCount > 0) {
    const cls =
      tone === "emerald"
        ? "bg-emerald-900/60 text-emerald-300"
        : `bg-zinc-800/80 ${entityText ?? "text-zinc-300"}`;
    return (
      <span
        className={`rounded-full ${cls} px-1.5 py-0.5 text-[9px] font-bold tabular-nums`}
        aria-label={`${childCount} children`}
      >
        {childCount}
      </span>
    );
  }
  return null;
}

// ── Placeholder node (referenced but missing from vault) ─────────────────────

export type PlaceholderNodeData = {
  kind: "placeholder";
  label: string;
};

export function PlaceholderNodeView({
  data,
}: NodeProps<Node<Record<string, unknown>>>) {
  const d = data as unknown as PlaceholderNodeData;
  return (
    <div
      className="relative rounded border border-dashed border-zinc-700 bg-zinc-900/20 px-2.5 py-1.5 opacity-50"
      style={{ width: ENTITY_NODE_WIDTH, minHeight: ENTITY_NODE_HEIGHT }}
      title="Entity not found in vault"
    >
      <Handle type="target" position={Position.Top} className="!bg-zinc-700" />
      <div className="truncate font-mono text-[10px] text-zinc-500">{d.label}</div>
      <div className="text-[9px] uppercase text-zinc-600">not found</div>
      <Handle type="source" position={Position.Bottom} className="!bg-zinc-700" />
    </div>
  );
}

// ── Helpers ───────────────────────────────────────────────────────────────────

function entityTypeColors(type: string): {
  bg: string;
  border: string;
  text: string;
} {
  switch (type) {
    case "service":
      return { bg: "bg-sky-950/30", border: "border-sky-900", text: "text-sky-300" };
    case "error":
      return { bg: "bg-rose-950/30", border: "border-rose-900", text: "text-rose-300" };
    case "symptom":
      return { bg: "bg-amber-950/30", border: "border-amber-900", text: "text-amber-300" };
    case "component":
      return {
        bg: "bg-zinc-900/40",
        border: "border-zinc-700",
        text: "text-zinc-300",
      };
    default:
      return { bg: "bg-zinc-900/40", border: "border-zinc-700", text: "text-zinc-400" };
  }
}
