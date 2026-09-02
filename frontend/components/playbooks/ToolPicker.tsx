"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import type { ToolEntry } from "@/lib/api";

type Props = {
  // Current tool wire-name, in the form "<server>/<tool>" (matches the
  // playbook YAML schema's `tool:` field, e.g. "triagent-k8s/list_resources").
  // Empty string means no selection yet.
  value: string;
  onChange: (next: string) => void;
  catalog: ToolEntry[];
  // Marks the picker disabled (read-only system playbook view).
  disabled?: boolean;
};

// ToolPicker is a searchable dropdown over the triagent-mcp catalog. The wire
// format the playbook YAML stores is "<server>/<tool>" — the picker
// hides that detail behind a "<server>: <tool>" rendering. Free-form
// entries are accepted (so an in-flight rename doesn't break the editor)
// but flagged with a small warning chip.
export function ToolPicker({ value, onChange, catalog, disabled }: Props) {
  const [open, setOpen] = useState(false);
  const [filter, setFilter] = useState("");
  const popoverRef = useRef<HTMLDivElement | null>(null);

  // Close the popover when clicking outside.
  useEffect(() => {
    if (!open) return;
    function onClick(e: MouseEvent) {
      if (popoverRef.current && !popoverRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener("mousedown", onClick);
    return () => document.removeEventListener("mousedown", onClick);
  }, [open]);

  const grouped = useMemo(() => {
    const q = filter.trim().toLowerCase();
    const map = new Map<string, ToolEntry[]>();
    for (const t of catalog) {
      if (q && !`${t.server}/${t.name}`.toLowerCase().includes(q) && !t.description.toLowerCase().includes(q)) {
        continue;
      }
      const list = map.get(t.server) ?? [];
      list.push(t);
      map.set(t.server, list);
    }
    return Array.from(map.entries()).sort(([a], [b]) => a.localeCompare(b));
  }, [catalog, filter]);

  const known = useMemo(
    () => catalog.some((t) => `${t.server}/${t.name}` === value),
    [catalog, value],
  );

  const display = value || "(pick a tool)";

  return (
    <div className="relative" ref={popoverRef}>
      <button
        type="button"
        disabled={disabled}
        onClick={() => setOpen((o) => !o)}
        className={
          "flex w-full items-center justify-between gap-2 rounded border px-2 py-1.5 text-left text-xs font-mono transition " +
          (disabled
            ? "border-zinc-800 bg-zinc-950 text-zinc-500"
            : "border-zinc-700 bg-zinc-950 text-sky-300 hover:border-zinc-500")
        }
      >
        <span className="truncate">{display}</span>
        <span className="flex items-center gap-1.5">
          {value && !known && (
            <span
              title="Tool not in current catalog — typo, renamed, a repo that isn't linked, or from a newer triagent-mcp version"
              className="rounded bg-amber-900/60 px-1 text-xs text-amber-200"
            >
              unknown
            </span>
          )}
          <span className="text-zinc-500">▾</span>
        </span>
      </button>

      {open && (
        <div className="absolute left-0 right-0 top-full z-20 mt-1 max-h-80 overflow-y-auto rounded border border-zinc-700 bg-zinc-950 shadow-lg">
          <input
            autoFocus
            type="text"
            placeholder="filter tools…"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            className="sticky top-0 w-full border-b border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs placeholder-zinc-600 focus:outline-none"
          />
          {grouped.length === 0 && (
            <div className="px-2 py-2 text-xs text-zinc-500">no matches</div>
          )}
          {grouped.map(([server, tools], i) => (
            <div
              key={server}
              className={i > 0 ? "border-t-2 border-zinc-700" : ""}
            >
              <div className="sticky top-7 z-10 flex items-center gap-2 border-b border-zinc-800 bg-zinc-900 px-2 py-1.5 font-mono text-sm font-semibold text-sky-300">
                <span className="inline-block h-1.5 w-1.5 rounded-full bg-sky-400" />
                {server}
                <span className="text-xs font-normal text-zinc-500">
                  {tools.length} {tools.length === 1 ? "tool" : "tools"}
                </span>
              </div>
              <ul>
                {tools.map((t) => {
                  const wire = `${t.server}/${t.name}`;
                  const active = wire === value;
                  return (
                    <li key={wire}>
                      <button
                        type="button"
                        onClick={() => {
                          onChange(wire);
                          setOpen(false);
                        }}
                        className={
                          "flex w-full flex-col items-start gap-0.5 px-2 py-1.5 text-left transition " +
                          (active
                            ? "bg-zinc-800/80"
                            : "hover:bg-zinc-900/60")
                        }
                      >
                        <span className="font-mono text-sm text-zinc-100">
                          {t.name}
                        </span>
                        <span className="text-xs text-zinc-500">
                          {t.description}
                        </span>
                      </button>
                    </li>
                  );
                })}
              </ul>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
