"use client";

import { useMemo, useState } from "react";
import type { Investigation } from "@/lib/api";
import { parseToolName, type TranscriptItem } from "@/lib/events";
import { ChevronLeftIcon, ChevronRightIcon } from "./Icons";
import { MCPStatusBar } from "./MCPStatusBar";
import { toolAnchorId } from "./ToolCard";

type Props = {
  investigation: Investigation;
  items: TranscriptItem[];
  // onCollapse hides the panel down to a thin rail (ActivityRail) so
  // the operator can claim more horizontal space for the chat. When
  // omitted the collapse cue isn't rendered.
  onCollapse?: () => void;
};

// Pretty-prints a server alias for the chip / row label. The MCP wire
// names (triagent-k8s, triagent-strategies, prom, example-docs) are stable and
// short enough to show as-is.
function serverLabel(server: string | null): string {
  return server ?? "other";
}

// FlatCall is the activity panel's row shape. Top-level tool_calls
// flatten directly; nested sub-agent tool events (e.g. the per-file
// reads triagent-git/analyze_change spawns) flatten as additional rows
// with parentToolId set so the renderer can indent them under their
// parent. parentTopId is always the top-level parent's toolId — the
// click-to-jump target, since nested events render INSIDE the parent
// ToolCard rather than as their own anchor.
type FlatCall = {
  id: string;
  toolId: string;
  name: string;
  input?: Record<string, unknown>;
  result?: string;
  startedAt: string;
  endedAt?: string;
  server: string;
  parentToolId?: string;
  parentTopId: string;
};

// extractCalls flattens the transcript down to the tool_call items
// we care about — top-level + their nested sub-agent tool events —
// in transcript order, with their parsed server alias attached.
function extractCalls(items: TranscriptItem[]): FlatCall[] {
  const out: FlatCall[] = [];
  for (const it of items) {
    if (it.kind !== "tool_call") continue;
    const { server } = parseToolName(it.name);
    out.push({
      id: it.id,
      toolId: it.toolId,
      name: it.name,
      input: it.input,
      result: it.result,
      startedAt: it.startedAt,
      endedAt: it.endedAt,
      server: serverLabel(server),
      parentTopId: it.toolId,
    });
    for (const child of it.children ?? []) {
      if (child.kind !== "tool") continue;
      const { server: childServer } = parseToolName(child.name);
      out.push({
        id: child.id,
        toolId: child.toolId,
        name: child.name,
        input: child.input,
        result: child.result,
        startedAt: child.ts,
        endedAt: child.tsEnd,
        server: serverLabel(childServer),
        parentToolId: it.toolId,
        parentTopId: it.toolId,
      });
    }
  }
  return out;
}

function durationMs(startedAt: string, endedAt?: string): number | null {
  if (!endedAt) return null;
  const ms = new Date(endedAt).getTime() - new Date(startedAt).getTime();
  return Number.isFinite(ms) ? Math.max(0, ms) : null;
}

function formatDuration(ms: number | null): string {
  if (ms === null) return "running…";
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  return `${(ms / 60_000).toFixed(1)} min`;
}

// AUTO_OPERATOR_TOOL_PREFIX identifies tool calls emitted by the
// auto-operator's MCP surface (triagent-agent-operator). The activity panel
// buckets these into their own collapsible group so the operator can
// see auto-driven actions distinct from the investigation agent's
// regular tool calls.
const AUTO_OPERATOR_TOOL_PREFIX = "mcp__triagent-agent-operator__";

export function ActivityPanel({ investigation, items, onCollapse }: Props) {
  const calls = useMemo(() => extractCalls(items), [items]);

  // Operator-driven tool calls live in a separate group at the top of
  // the panel. They share the wire-prefix `mcp__triagent-agent-operator__`;
  // filter by name rather than by server alias so a future rename of
  // the alias doesn't silently break the grouping.
  const operatorCalls = useMemo(
    () => calls.filter((c) => c.name.startsWith(AUTO_OPERATOR_TOOL_PREFIX)),
    [calls],
  );

  // Counters per server, regardless of filter, so the chips show real
  // totals.
  const counts = useMemo(() => {
    const map = new Map<string, number>();
    for (const c of calls) map.set(c.server, (map.get(c.server) ?? 0) + 1);
    return map;
  }, [calls]);

  const servers = useMemo(() => Array.from(counts.keys()).sort(), [counts]);

  // Filter state. Default: every server selected. As the AI hits new
  // servers we add them as enabled-by-default.
  const [enabled, setEnabled] = useState<Set<string>>(new Set());
  const enabledOrAll = useMemo(() => {
    if (enabled.size === 0) return new Set(servers);
    return enabled;
  }, [enabled, servers]);

  function toggle(server: string) {
    setEnabled((prev) => {
      const next = new Set(prev.size === 0 ? servers : prev);
      if (next.has(server)) next.delete(server);
      else next.add(server);
      return next;
    });
  }

  const visible = useMemo(
    () =>
      calls.filter(
        (c) =>
          enabledOrAll.has(c.server) &&
          !c.name.startsWith(AUTO_OPERATOR_TOOL_PREFIX),
      ),
    [calls, enabledOrAll],
  );

  function jumpTo(toolId: string) {
    const el = document.getElementById(toolAnchorId(toolId));
    if (!el) return;
    el.scrollIntoView({ behavior: "smooth", block: "center" });
    // Brief flash to confirm we landed on the right card. Tailwind ring
    // utility classes; matched timeout removes them.
    const flashClasses = ["ring-2", "ring-sky-400/60", "ring-offset-2", "ring-offset-zinc-950"];
    el.classList.add(...flashClasses);
    window.setTimeout(() => el.classList.remove(...flashClasses), 1500);
  }

  return (
    <aside
      data-testid="triagent-activity-panel"
      className="flex w-80 shrink-0 flex-col gap-3 border-l border-zinc-800 bg-zinc-950 px-3 py-4"
    >
      {onCollapse && (
        <div className="flex justify-end">
          <button
            type="button"
            onClick={onCollapse}
            aria-label="collapse activity panel"
            title="collapse activity panel"
            className="inline-flex h-6 w-6 items-center justify-center rounded text-zinc-500 transition hover:bg-zinc-900 hover:text-zinc-200"
          >
            <ChevronRightIcon className="h-3.5 w-3.5" />
          </button>
        </div>
      )}
      <MCPStatusBar investigation={investigation} />

      <div
        className={
          "px-1" +
          (investigation.archived ? "" : " border-t border-zinc-800/60 pt-3")
        }
      >
        <h3 className="text-sm font-medium text-zinc-200">activity</h3>
        <p className="mt-0.5 text-xs text-zinc-500">
          {calls.length === 0
            ? "no tool calls yet"
            : `${calls.length} tool ${calls.length === 1 ? "call" : "calls"}`}
        </p>
      </div>

      {servers.length > 0 && (
        <div className="flex flex-wrap gap-1.5 px-1">
          {servers.map((s) => {
            const on = enabledOrAll.has(s);
            return (
              <button
                key={s}
                type="button"
                onClick={() => toggle(s)}
                className={
                  "rounded-full border px-2 py-0.5 text-xs font-mono transition " +
                  (on
                    ? "border-zinc-600 bg-zinc-800 text-zinc-100"
                    : "border-zinc-800 text-zinc-500 hover:text-zinc-300")
                }
              >
                {s} <span className="text-zinc-500">{counts.get(s)}</span>
              </button>
            );
          })}
        </div>
      )}

      <div className="flex-1 overflow-y-auto pr-1">
        {calls.length === 0 && (
          <p className="px-1 text-xs text-zinc-500">
            tool calls show up here as the agent works.
          </p>
        )}
        {operatorCalls.length > 0 && (
          // Auto-operator group — collapsible so an operator who only
          // cares about the investigation agent's tool calls can fold
          // it away. Pink border mirrors the auto-operator chrome
          // elsewhere in the UI (SessionView envelope border, sidebar
          // glyph) so the visual language is consistent.
          <details className="mb-2 rounded border border-pink-500/40 p-2">
            <summary className="cursor-pointer text-xs font-medium uppercase tracking-wide text-pink-400">
              Auto-operator ({operatorCalls.length})
            </summary>
            <ul className="mt-1 space-y-1">
              {operatorCalls.map((c) => (
                <li key={c.id}>
                  <ActivityRow
                    call={c}
                    nested={c.parentToolId !== undefined}
                    onClick={() => jumpTo(c.parentTopId)}
                  />
                </li>
              ))}
            </ul>
          </details>
        )}
        <ul className="space-y-1">
          {visible.map((c) => (
            <li key={c.id}>
              <ActivityRow
                call={c}
                nested={c.parentToolId !== undefined}
                onClick={() => jumpTo(c.parentTopId)}
              />
            </li>
          ))}
        </ul>
      </div>
    </aside>
  );
}

function ActivityRow({
  call,
  nested,
  onClick,
}: {
  call: FlatCall;
  nested: boolean;
  onClick: () => void;
}) {
  const { short } = parseToolName(call.name);
  const ms = durationMs(call.startedAt, call.endedAt);
  const status = call.result === undefined ? "running" : "ok";
  const summary = oneLineSummary(call.input);

  return (
    <button
      type="button"
      data-testid="triagent-activity-row"
      data-tool-id={call.toolId}
      onClick={onClick}
      // `group` lets the inner name span flip from truncate → wrap on
      // hover so the row expands vertically and reveals the full tool
      // name. Long mcp wire names (mcp__triagent-strategies__…_draft) get
      // clipped at rest; hover gives the operator the full id without
      // having to open the parent ToolCard.
      className={
        "group block w-full rounded px-2 py-1.5 text-left transition hover:bg-zinc-900 " +
        (nested ? "ml-4 border-l border-zinc-800/80 pl-2" : "")
      }
    >
      <div className="flex items-baseline justify-between gap-2">
        <span className="flex items-baseline gap-2 min-w-0">
          <Dot status={status} />
          <span
            className={
              "truncate font-mono text-xs group-hover:overflow-visible group-hover:whitespace-normal group-hover:break-all " +
              (nested ? "text-sky-300/70" : "text-sky-300")
            }
          >
            {call.server !== "other" && (
              <span className="text-zinc-500">{call.server}/</span>
            )}
            {short}
          </span>
        </span>
        <span className="shrink-0 font-mono text-xs text-zinc-500">
          {formatDuration(ms)}
        </span>
      </div>
      {summary && (
        <div className="mt-0.5 truncate pl-3.5 font-mono text-xs text-zinc-500">
          {summary}
        </div>
      )}
    </button>
  );
}

function Dot({ status }: { status: "running" | "ok" }) {
  if (status === "running") {
    return (
      <span
        aria-hidden
        className="inline-block h-2 w-2 animate-pulse rounded-full bg-sky-400"
      />
    );
  }
  return (
    <span
      aria-hidden
      className="inline-block h-2 w-2 rounded-full bg-emerald-400"
    />
  );
}

function oneLineSummary(input: Record<string, unknown> | null | undefined): string {
  if (!input) return "";
  const parts: string[] = [];
  for (const [k, v] of Object.entries(input)) {
    let val = typeof v === "string" ? v : JSON.stringify(v);
    if (val.length > 32) val = val.slice(0, 29) + "…";
    parts.push(`${k}=${val}`);
  }
  const joined = parts.join(" ");
  return joined.length > 80 ? joined.slice(0, 77) + "…" : joined;
}

// ActivityRail is the collapsed-state stand-in for ActivityPanel:
// a thin vertical column on the right edge with an expand chevron and
// a vertical "activity" label. Click anywhere on it to re-open the
// full panel.
export function ActivityRail({ onExpand }: { onExpand: () => void }) {
  return (
    <button
      type="button"
      onClick={onExpand}
      aria-label="expand activity panel"
      title="expand activity panel"
      className="group flex w-8 shrink-0 flex-col items-center gap-3 border-l border-zinc-800 bg-zinc-950 py-3 text-zinc-500 transition hover:bg-zinc-900 hover:text-zinc-200"
    >
      <ChevronLeftIcon className="h-3.5 w-3.5" />
      <span
        className="select-none text-[10px] uppercase tracking-widest"
        // Vertical text column; CSS keeps the letters readable from
        // top-to-bottom along the rail.
        style={{ writingMode: "vertical-rl" }}
      >
        activity
      </span>
    </button>
  );
}
