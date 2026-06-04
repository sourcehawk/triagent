"use client";

import { argInputClass } from "./NodeEditor.fields";

// HandoffList renders the list of cross-playbook target ids on a
// terminal node. Each entry is a dropdown bound to the loaded
// playbook-id set (so typos can't sneak in for the common case);
// dangling ids — a saved playbook references a target that's since
// been renamed or deleted — surface as a red "(dangling)" option so
// the operator can fix or remove them. The "open" affordance navigates
// the editor to that playbook without leaving the page.
export function HandoffList({
  values,
  allPlaybookIds,
  readOnly,
  onOpen,
  onChange,
}: {
  values: string[];
  allPlaybookIds: string[];
  readOnly: boolean;
  onOpen: (id: string) => void;
  onChange: (next: string[]) => void;
}) {
  return (
    <div className="space-y-1">
      {values.length === 0 && (
        <p className="text-xs text-zinc-500">
          No handoffs. Add one when this terminal hands off to another playbook
          (operator + agent get a clickable link instead of buried prose).
        </p>
      )}
      {values.map((v, i) => {
        const dangling = v !== "" && !allPlaybookIds.includes(v);
        return (
          <div key={i} className="flex items-center gap-1.5">
            <select
              value={v}
              disabled={readOnly}
              onChange={(e) => {
                const next = [...values];
                next[i] = e.target.value;
                onChange(next);
              }}
              className={`${argInputClass} flex-1 bg-zinc-950 ${dangling ? "border-red-900/70 text-red-200" : ""}`}
            >
              <option value="">— pick a target playbook —</option>
              {allPlaybookIds.map((id) => (
                <option key={id} value={id}>
                  {id}
                </option>
              ))}
              {dangling && (
                <option value={v}>{v} (dangling — not loaded)</option>
              )}
            </select>
            {v && !dangling && (
              <button
                type="button"
                onClick={() => onOpen(v)}
                title={`open ${v} in editor`}
                className="rounded border border-zinc-800 px-1.5 py-0.5 text-xs text-zinc-400 transition hover:border-zinc-600 hover:text-zinc-100"
              >
                open →
              </button>
            )}
            {!readOnly && (
              <button
                type="button"
                onClick={() => onChange(values.filter((_, j) => j !== i))}
                className="text-xs text-red-400 hover:text-red-300"
              >
                ✕
              </button>
            )}
          </div>
        );
      })}
      {!readOnly && (
        <button
          type="button"
          onClick={() => onChange([...values, ""])}
          className="text-xs text-zinc-500 hover:text-zinc-300"
        >
          + add handoff
        </button>
      )}
    </div>
  );
}

// DelegateToPicker renders a single-target playbook-id dropdown for the
// delegate_to field. Shape mirrors HandoffList's row format (dropdown +
// optional "open →" + clear button) but constrained to a single value
// rather than a list, since delegate_to is `string?` not `string[]`.
export function DelegateToPicker({
  value,
  allPlaybookIds,
  readOnly,
  onOpen,
  onChange,
}: {
  value: string;
  allPlaybookIds: string[];
  readOnly: boolean;
  onOpen: (id: string) => void;
  onChange: (next: string) => void;
}) {
  const dangling = value !== "" && !allPlaybookIds.includes(value);
  return (
    <div className="space-y-1">
      <p className="text-xs text-zinc-500">
        Sub-flow: walk another playbook to its non-handoff terminal, then
        resume this node&apos;s <code className="font-mono">next</code>.
        Findings recorded inside the sub-flow flow into the same session.
      </p>
      <div className="flex items-center gap-1.5">
        <select
          value={value}
          disabled={readOnly}
          onChange={(e) => onChange(e.target.value)}
          className={`${argInputClass} flex-1 bg-zinc-950 ${dangling ? "border-red-900/70 text-red-200" : ""}`}
        >
          <option value="">— no sub-flow (regular node) —</option>
          {allPlaybookIds.map((id) => (
            <option key={id} value={id}>
              {id}
            </option>
          ))}
          {dangling && (
            <option value={value}>{value} (dangling — not loaded)</option>
          )}
        </select>
        {value && !dangling && (
          <button
            type="button"
            onClick={() => onOpen(value)}
            title={`open ${value} in editor`}
            className="rounded border border-zinc-800 px-1.5 py-0.5 text-xs text-zinc-400 transition hover:border-zinc-600 hover:text-zinc-100"
          >
            open →
          </button>
        )}
        {!readOnly && value && (
          <button
            type="button"
            onClick={() => onChange("")}
            className="text-xs text-red-400 hover:text-red-300"
          >
            ✕
          </button>
        )}
      </div>
    </div>
  );
}
