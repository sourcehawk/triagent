"use client";

import type { WatchFilter, WatchSourceKind } from "@/lib/api";

const FIELDS: Record<WatchSourceKind, string[]> = {
  github_issues: ["title", "body", "author", "label"],
  slack_channel: ["text", "author"],
};
const OPS = ["contains", "does_not_contain", "regex_matches", "not_regex_matches"] as const;

const selectClass =
  "rounded border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-sm text-zinc-100 focus:border-zinc-600 focus:outline-none";

export function FilterBuilder({
  kind,
  value,
  onChange,
}: {
  kind: WatchSourceKind;
  value: WatchFilter[];
  onChange: (next: WatchFilter[]) => void;
}) {
  const fields = FIELDS[kind];
  function update(i: number, patch: Partial<WatchFilter>) {
    onChange(value.map((f, idx) => idx === i ? { ...f, ...patch } : f));
  }
  function add() {
    onChange([...value, { field: fields[0], op: "contains", value: "" }]);
  }
  function remove(i: number) {
    onChange(value.filter((_, idx) => idx !== i));
  }
  return (
    <div className="space-y-2">
      {value.length === 0 && (
        <div className="text-xs text-zinc-500">
          No filters — every item from the source reaches the ingestion agent.
        </div>
      )}
      {value.map((f, i) => (
        <div key={i} className="flex items-center gap-2">
          <select
            aria-label={`field-${i}`}
            value={f.field}
            onChange={e => update(i, { field: e.target.value })}
            className={selectClass}
          >
            {fields.map(fd => <option key={fd} value={fd}>{fd}</option>)}
          </select>
          <select
            aria-label={`op-${i}`}
            value={f.op}
            onChange={e => update(i, { op: e.target.value as WatchFilter["op"] })}
            className={selectClass}
          >
            {OPS.map(op => <option key={op} value={op}>{op}</option>)}
          </select>
          <input
            aria-label={`value-${i}`}
            value={f.value}
            onChange={e => update(i, { value: e.target.value })}
            className="flex-1 rounded border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-sm text-zinc-100 placeholder-zinc-600 focus:border-zinc-600 focus:outline-none"
          />
          <button
            type="button"
            onClick={() => remove(i)}
            className="text-zinc-500 transition hover:text-red-400"
            aria-label={`remove filter ${i}`}
          >
            ×
          </button>
        </div>
      ))}
      <button
        type="button"
        onClick={add}
        className="inline-flex items-center gap-1.5 rounded border border-zinc-700 bg-zinc-900/60 px-3 py-1.5 text-xs text-zinc-300 transition hover:border-zinc-600 hover:text-zinc-100"
      >
        + Add filter
      </button>
    </div>
  );
}
