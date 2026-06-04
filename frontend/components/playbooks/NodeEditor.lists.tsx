"use client";

import type { Branch, SuggestedCall } from "@/lib/playbook";
import type { ToolEntry } from "@/lib/api";
import { ToolPicker } from "@/components/playbooks/ToolPicker";
import { argInputClass } from "./NodeEditor.fields";
import { AvailableInputs, ArgsEditor } from "./NodeEditor.inputs";

export function SuggestedCallList({
  calls,
  catalog,
  readOnly,
  onChange,
}: {
  calls: SuggestedCall[];
  catalog: ToolEntry[];
  readOnly: boolean;
  onChange: (next: SuggestedCall[]) => void;
}) {
  return (
    <div className="space-y-2">
      {calls.length === 0 && (
        <p className="text-xs text-zinc-500">No suggested calls.</p>
      )}
      {calls.map((c, i) => (
        <div
          key={i}
          className="space-y-2 rounded border border-zinc-800 bg-zinc-950/40 p-2"
        >
          <ToolPicker
            value={c.tool}
            disabled={readOnly}
            catalog={catalog}
            onChange={(next) => {
              const arr = [...calls];
              // Auto-prefill required inputs when the tool first
              // gets picked (or changes) — operators almost always
              // need to fill those in, so saving the manual click
              // for each is a real ergonomic win. Optional inputs
              // stay opt-in below.
              const tool = catalog.find((t) => `${t.server}/${t.name}` === next);
              const prefilled: Record<string, unknown> = {};
              for (const inp of tool?.inputs ?? []) {
                if (inp.required) prefilled[inp.name] = "";
              }
              arr[i] = {
                ...arr[i],
                tool: next,
                args: Object.keys(prefilled).length > 0 ? prefilled : undefined,
              };
              onChange(arr);
            }}
          />
          <AvailableInputs
            tool={catalog.find(
              (t) => `${t.server}/${t.name}` === c.tool,
            )}
            args={c.args ?? {}}
            readOnly={readOnly}
            onAdd={(name) => {
              const arr = [...calls];
              const next = { ...(arr[i].args ?? {}), [name]: "" };
              arr[i] = { ...arr[i], args: next };
              onChange(arr);
            }}
          />
          <ArgsEditor
            value={c.args ?? {}}
            readOnly={readOnly}
            onChange={(args) => {
              const arr = [...calls];
              arr[i] = {
                ...arr[i],
                args: Object.keys(args).length === 0 ? undefined : args,
              };
              onChange(arr);
            }}
          />
          {!readOnly && (
            <div className="flex gap-2">
              <button
                type="button"
                onClick={() => {
                  if (i === 0) return;
                  const arr = [...calls];
                  [arr[i - 1], arr[i]] = [arr[i], arr[i - 1]];
                  onChange(arr);
                }}
                className="text-xs text-zinc-500 hover:text-zinc-300"
              >
                ↑
              </button>
              <button
                type="button"
                onClick={() => {
                  if (i === calls.length - 1) return;
                  const arr = [...calls];
                  [arr[i + 1], arr[i]] = [arr[i], arr[i + 1]];
                  onChange(arr);
                }}
                className="text-xs text-zinc-500 hover:text-zinc-300"
              >
                ↓
              </button>
              <button
                type="button"
                onClick={() => onChange(calls.filter((_, j) => j !== i))}
                className="ml-auto text-xs text-red-400 hover:text-red-300"
              >
                remove
              </button>
            </div>
          )}
        </div>
      ))}
      {!readOnly && (
        <button
          type="button"
          onClick={() => onChange([...calls, { tool: "" }])}
          className="rounded border border-dashed border-zinc-700 px-2 py-1 text-xs text-zinc-400 transition hover:border-zinc-500 hover:text-zinc-200"
        >
          + add call
        </button>
      )}
    </div>
  );
}

export function StringList({
  values,
  readOnly,
  onChange,
  placeholder,
}: {
  values: string[];
  readOnly: boolean;
  onChange: (next: string[]) => void;
  placeholder?: string;
}) {
  return (
    <div className="space-y-1">
      {values.length === 0 && (
        <p className="text-xs text-zinc-500">No entries.</p>
      )}
      {values.map((v, i) => (
        <div key={i} className="flex gap-1.5">
          <input
            value={v}
            disabled={readOnly}
            placeholder={placeholder}
            onChange={(e) => {
              const next = [...values];
              next[i] = e.target.value;
              onChange(next);
            }}
            className={`${argInputClass} flex-1`}
          />
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
      ))}
      {!readOnly && (
        <button
          type="button"
          onClick={() => onChange([...values, ""])}
          className="text-xs text-zinc-500 hover:text-zinc-300"
        >
          + add
        </button>
      )}
    </div>
  );
}

export function BranchList({
  branches,
  allNodeIds,
  readOnly,
  onChange,
}: {
  branches: Branch[];
  allNodeIds: string[];
  readOnly: boolean;
  onChange: (next: Branch[]) => void;
}) {
  return (
    <div className="space-y-2">
      {branches.length === 0 && (
        <p className="text-xs text-zinc-500">
          No outgoing branches — terminal node.
        </p>
      )}
      {branches.map((b, i) => (
        <div
          key={i}
          className="space-y-1 rounded border border-zinc-800 bg-zinc-950/40 p-2"
        >
          <input
            value={b.condition}
            disabled={readOnly}
            placeholder="condition (prose; agent reads to decide)"
            onChange={(e) => {
              const next = [...branches];
              next[i] = { ...next[i], condition: e.target.value };
              onChange(next);
            }}
            className={argInputClass}
          />
          <select
            value={b.goto}
            disabled={readOnly}
            onChange={(e) => {
              const next = [...branches];
              next[i] = { ...next[i], goto: e.target.value };
              onChange(next);
            }}
            className={`${argInputClass} bg-zinc-950`}
          >
            <option value="">— pick a target node —</option>
            {allNodeIds.map((id) => (
              <option key={id} value={id}>
                {id}
              </option>
            ))}
            {b.goto && !allNodeIds.includes(b.goto) && (
              <option value={b.goto}>{b.goto} (dangling)</option>
            )}
          </select>
          {!readOnly && (
            <div className="flex justify-end">
              <button
                type="button"
                onClick={() => onChange(branches.filter((_, j) => j !== i))}
                className="text-xs text-red-400 hover:text-red-300"
              >
                remove branch
              </button>
            </div>
          )}
        </div>
      ))}
      {!readOnly && (
        <button
          type="button"
          onClick={() => onChange([...branches, { condition: "", goto: "" }])}
          className="rounded border border-dashed border-zinc-700 px-2 py-1 text-xs text-zinc-400 transition hover:border-zinc-500 hover:text-zinc-200"
        >
          + add branch
        </button>
      )}
    </div>
  );
}
