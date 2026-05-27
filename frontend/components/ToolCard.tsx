"use client";

import { useState } from "react";
import { parseToolName, type NestedEvent } from "@/lib/events";

type Props = {
  toolId: string;
  name: string;
  input?: Record<string, unknown> | null;
  result?: string;
  pending: boolean;
  nested?: NestedEvent[];
};

function summariseInput(
  input: Record<string, unknown> | null | undefined,
): string {
  if (!input) return "";
  const parts: string[] = [];
  for (const [k, v] of Object.entries(input)) {
    let val = typeof v === "string" ? v : JSON.stringify(v);
    if (val.length > 60) val = val.slice(0, 57) + "…";
    parts.push(`${k}=${val}`);
  }
  return parts.join(" ");
}

export function ToolCard({ toolId, name, input, result, pending, nested }: Props) {
  const [open, setOpen] = useState(false);
  const summary = summariseInput(input);
  const { short } = parseToolName(name);
  const childCount = nested?.length ?? 0;

  return (
    <div
      id={toolAnchorId(toolId)}
      className="rounded border border-zinc-800 bg-zinc-900/40 text-sm transition-shadow"
    >
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-baseline justify-between gap-3 px-3 py-2 text-left transition hover:bg-zinc-900/60"
      >
        <span className="flex items-baseline gap-2 min-w-0">
          <span aria-hidden className="text-xs text-zinc-500">
            {open ? "▾" : "▸"}
          </span>
          <span className="font-mono text-xs font-medium text-sky-300">
            {short}
          </span>
          {summary && (
            <span className="font-mono text-xs text-zinc-500 truncate">
              {summary}
            </span>
          )}
          {childCount > 0 && (
            <span
              className="shrink-0 whitespace-nowrap rounded-full bg-zinc-800 px-1.5 py-0.5 text-xs font-medium text-zinc-300"
              title={`${childCount} sub-agent event${childCount === 1 ? "" : "s"}`}
            >
              {childCount} nested
            </span>
          )}
        </span>
        <span
          className={
            pending
              ? "text-xs text-zinc-500"
              : result
                ? "text-xs text-emerald-400"
                : "text-xs text-zinc-500"
          }
        >
          {pending ? "running…" : result ? "ok" : "—"}
        </span>
      </button>

      {open && (
        <div className="space-y-2 border-t border-zinc-800 px-3 py-3">
          {input && Object.keys(input).length > 0 && (
            <Section label="input">
              <pre className="overflow-x-auto rounded bg-zinc-950 p-2 font-mono text-xs text-zinc-200">
                {JSON.stringify(input, null, 2)}
              </pre>
            </Section>
          )}
          {childCount > 0 && (
            <Section label={`sub-agent activity · ${childCount}`}>
              <ul className="space-y-1.5">
                {nested!.map((c) => (
                  <li key={c.id}>
                    <NestedRow ev={c} />
                  </li>
                ))}
              </ul>
            </Section>
          )}
          {result !== undefined && (
            <Section label="result">
              <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-words rounded bg-zinc-950 p-2 font-mono text-xs text-zinc-200">
                {result || "(empty)"}
              </pre>
            </Section>
          )}
          {pending && (
            <p className="text-xs text-zinc-500">waiting for tool result…</p>
          )}
        </div>
      )}
    </div>
  );
}

function NestedRow({ ev }: { ev: NestedEvent }) {
  const [open, setOpen] = useState(false);
  if (ev.kind === "status") {
    return (
      <div className="px-2 py-0.5 text-xs italic text-zinc-500">
        {ev.text}
      </div>
    );
  }
  if (ev.kind === "text") {
    return (
      <div className="rounded border border-zinc-800/60 bg-zinc-950/40 px-2 py-1.5 text-xs text-zinc-300">
        <div className="mb-0.5 text-xs uppercase tracking-wide text-zinc-500">
          subagent
        </div>
        <div className="whitespace-pre-wrap break-words">{ev.text}</div>
      </div>
    );
  }
  // tool kind
  const { short } = parseToolName(ev.name);
  const summary = summariseInput(ev.input);
  const pending = ev.result === undefined;
  return (
    <div className="rounded border border-zinc-800/60 bg-zinc-950/40">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-baseline justify-between gap-2 px-2 py-1.5 text-left transition hover:bg-zinc-900/60"
      >
        <span className="flex items-baseline gap-2 min-w-0">
          <span aria-hidden className="text-xs text-zinc-500">
            {open ? "▾" : "▸"}
          </span>
          <span className="font-mono text-xs font-medium text-sky-300/90">
            {short}
          </span>
          {summary && (
            <span className="font-mono text-xs text-zinc-500 truncate">
              {summary}
            </span>
          )}
        </span>
        <span
          className={
            pending
              ? "text-xs text-zinc-500"
              : "text-xs text-emerald-400/80"
          }
        >
          {pending ? "running…" : "ok"}
        </span>
      </button>
      {open && (ev.input || ev.result !== undefined) && (
        <div className="space-y-1.5 border-t border-zinc-800/60 px-2 py-1.5">
          {ev.input && Object.keys(ev.input).length > 0 && (
            <pre className="overflow-x-auto rounded bg-zinc-950 p-1.5 font-mono text-xs text-zinc-300">
              {JSON.stringify(ev.input, null, 2)}
            </pre>
          )}
          {ev.result !== undefined && (
            <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded bg-zinc-950 p-1.5 font-mono text-xs text-zinc-300">
              {ev.result || "(empty)"}
            </pre>
          )}
        </div>
      )}
    </div>
  );
}

// toolAnchorId is the DOM id used for click-to-scroll from the activity
// panel. Exported so the panel uses the same string.
export function toolAnchorId(toolId: string): string {
  return `tool-${toolId}`;
}

function Section({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <div className="mb-1 text-xs uppercase tracking-wide text-zinc-500">
        {label}
      </div>
      {children}
    </div>
  );
}
