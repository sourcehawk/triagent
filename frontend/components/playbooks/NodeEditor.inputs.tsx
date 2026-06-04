"use client";

import type { ToolEntry } from "@/lib/api";
import { argInputClass } from "./NodeEditor.fields";

// AvailableInputs renders the picked tool's input shape as a list
// of opt-in rows. Required inputs are auto-prefilled when the tool
// is selected (see SuggestedCallList's onChange); this surface lists
// the optional ones (and re-adds dropped requireds) with a one-click
// "+ add" affordance per row. Rows that are already present in
// `args` are dimmed and disabled. Tools without an entry in the
// catalog (e.g. an unknown server) render nothing — the operator
// can still type free-form args via ArgsEditor below.
export function AvailableInputs({
  tool,
  args,
  readOnly,
  onAdd,
}: {
  tool?: ToolEntry;
  args: Record<string, unknown>;
  readOnly: boolean;
  onAdd: (name: string) => void;
}) {
  if (!tool || !tool.inputs || tool.inputs.length === 0) {
    return null;
  }
  return (
    <div className="rounded border border-zinc-800/80 bg-zinc-900/30 px-2 py-1.5">
      <div className="mb-1 text-xs uppercase tracking-wide text-zinc-500">
        available inputs
      </div>
      <ul className="space-y-1">
        {tool.inputs.map((inp) => {
          const present = inp.name in args;
          return (
            <li key={inp.name} className="text-xs">
              <div className="flex items-baseline gap-1.5">
                <span
                  className={
                    "font-mono " +
                    (present ? "text-zinc-500 line-through" : "text-zinc-200")
                  }
                >
                  {inp.name}
                </span>
                <span className="font-mono text-xs text-zinc-600">
                  {inp.type}
                </span>
                {inp.required && (
                  <span className="rounded bg-amber-900/40 px-1 py-px text-xs font-medium uppercase tracking-wide text-amber-300">
                    required
                  </span>
                )}
                {!present && !readOnly && (
                  <button
                    type="button"
                    onClick={() => onAdd(inp.name)}
                    className="ml-auto rounded border border-zinc-700 px-1.5 py-px text-xs text-zinc-300 transition hover:border-zinc-500 hover:text-zinc-100"
                  >
                    + add
                  </button>
                )}
                {present && (
                  <span className="ml-auto text-xs text-zinc-600">
                    added
                  </span>
                )}
              </div>
              {inp.description && (
                <p className="mt-0.5 line-clamp-2 text-xs leading-relaxed text-zinc-500">
                  {inp.description}
                </p>
              )}
            </li>
          );
        })}
      </ul>
    </div>
  );
}

export function ArgsEditor({
  value,
  readOnly,
  onChange,
}: {
  value: Record<string, unknown>;
  readOnly: boolean;
  onChange: (next: Record<string, unknown>) => void;
}) {
  const entries = Object.entries(value);
  return (
    <div className="space-y-1">
      {entries.length === 0 && (
        <p className="text-xs text-zinc-500">No args.</p>
      )}
      {entries.map(([k, v], i) => (
        // arg name + value get their own line each so neither gets
        // squashed into a sliver. The remove (✕) sits beside the key
        // input — closest to the field it identifies.
        <div
          key={i}
          className="space-y-1 rounded border border-zinc-800/60 bg-zinc-950/40 p-1.5"
        >
          <div className="flex items-center gap-1.5">
            <input
              value={k}
              disabled={readOnly}
              placeholder="key"
              onChange={(e) => {
                const next: Record<string, unknown> = {};
                entries.forEach(([kk, vv], j) => {
                  next[i === j ? e.target.value : kk] = vv;
                });
                onChange(next);
              }}
              className={`${argInputClass} flex-1`}
            />
            {!readOnly && (
              <button
                type="button"
                onClick={() => {
                  const next: Record<string, unknown> = { ...value };
                  delete next[k];
                  onChange(next);
                }}
                title="remove arg"
                className="text-xs text-red-400 hover:text-red-300"
              >
                ✕
              </button>
            )}
          </div>
          <input
            value={typeof v === "string" ? v : JSON.stringify(v)}
            disabled={readOnly}
            placeholder="value (string or JSON)"
            onChange={(e) => {
              const next: Record<string, unknown> = { ...value };
              const raw = e.target.value;
              // Heuristic: if it parses as JSON, store the parsed value;
              // otherwise treat as a plain string (matches how YAML
              // unquoted strings round-trip).
              try {
                next[k] = JSON.parse(raw);
              } catch {
                next[k] = raw;
              }
              onChange(next);
            }}
            className={`${argInputClass} w-full`}
          />
        </div>
      ))}
      {!readOnly && (
        <button
          type="button"
          onClick={() =>
            onChange({ ...value, ["arg_" + entries.length]: "" })
          }
          className="text-xs text-zinc-500 hover:text-zinc-300"
        >
          + add arg
        </button>
      )}
    </div>
  );
}
