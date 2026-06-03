"use client";

import type { Branch, PlaybookNode, SuggestedCall } from "@/lib/playbook";
import type { ToolEntry } from "@/lib/api";
import { useDialog } from "@/lib/dialog";
import { ToolPicker } from "@/components/playbooks/ToolPicker";

type Props = {
  // null when no node is selected — show a hint to click a node.
  nodeId: string | null;
  node: PlaybookNode | null;
  // Existing node ids in the playbook, for the goto dropdown.
  allNodeIds: string[];
  // All known playbook ids (system + user) excluding the playbook being
  // edited. Drives the handoff dropdown so the operator picks a real
  // target instead of typing a free-text id.
  allPlaybookIds: string[];
  catalog: ToolEntry[];
  readOnly: boolean;
  onChange: (next: PlaybookNode) => void;
  onRename: (newId: string) => void;
  onDelete: () => void;
  // Navigate to another playbook in the editor — wired from the parent
  // (PlaybookEditor → PlaybookView's onOpen). Each handoff entry shows
  // a small "open" affordance that calls this.
  onOpenPlaybook: (id: string) => void;
};

export function NodeEditor({
  nodeId,
  node,
  allNodeIds,
  allPlaybookIds,
  catalog,
  readOnly,
  onChange,
  onRename,
  onDelete,
  onOpenPlaybook,
}: Props) {
  const dialog = useDialog();
  if (!nodeId || !node) {
    return (
      <div className="rounded border border-zinc-800 bg-zinc-900/30 p-4 text-sm text-zinc-500">
        Select a node in the graph to edit its properties.
      </div>
    );
  }

  const update = (patch: Partial<PlaybookNode>) =>
    onChange({ ...node, ...patch });

  return (
    <div className="space-y-4">
      <Field label="node id">
        <input
          value={nodeId}
          disabled={readOnly}
          onChange={(e) => onRename(e.target.value)}
          className={inputClass}
        />
      </Field>

      <Field label="description">
        <textarea
          value={node.description}
          disabled={readOnly}
          onChange={(e) => update({ description: e.target.value })}
          rows={5}
          className={`${inputClass} resize-y`}
        />
      </Field>

      <Field label="suggested_calls">
        <SuggestedCallList
          calls={node.suggested_calls ?? []}
          catalog={catalog}
          readOnly={readOnly}
          onChange={(next) =>
            update({ suggested_calls: next.length === 0 ? undefined : next })
          }
        />
      </Field>

      <Field label="expected_findings">
        <StringList
          values={node.expected_findings ?? []}
          readOnly={readOnly}
          onChange={(next) =>
            update({ expected_findings: next.length === 0 ? undefined : next })
          }
          placeholder="finding key (e.g. cpu_pressure_pods)"
        />
      </Field>

      <Field label="next branches">
        <BranchList
          branches={node.next ?? []}
          allNodeIds={allNodeIds.filter((id) => id !== nodeId)}
          readOnly={readOnly}
          onChange={(next) => update({ next: next.length === 0 ? undefined : next })}
        />
      </Field>

      <Field label="terminal_advice">
        <textarea
          value={node.terminal_advice ?? ""}
          disabled={readOnly}
          onChange={(e) =>
            update({ terminal_advice: e.target.value || undefined })
          }
          rows={4}
          placeholder="Only set on terminal nodes — the prose the walker hands back to the operator."
          className={`${inputClass} resize-y`}
        />
      </Field>

      <Field label="handoff (cross-playbook)">
        <HandoffList
          values={node.handoff ?? []}
          allPlaybookIds={allPlaybookIds}
          readOnly={readOnly}
          onOpen={onOpenPlaybook}
          onChange={(next) =>
            update({ handoff: next.length === 0 ? undefined : next })
          }
        />
      </Field>

      <Field label="delegate_to (sub-flow)">
        <DelegateToPicker
          value={node.delegate_to ?? ""}
          allPlaybookIds={allPlaybookIds}
          readOnly={readOnly}
          onOpen={onOpenPlaybook}
          onChange={(next) => update({ delegate_to: next || undefined })}
        />
      </Field>

      {!readOnly && (
        <div className="pt-2">
          <button
            type="button"
            onClick={async () => {
              const ok = await dialog.confirm({
                title: `Delete node "${nodeId}"?`,
                body: "Any branches pointing here will become dangling — you'll need to fix or remove them before saving.",
                confirmLabel: "Delete",
                danger: true,
              });
              if (ok) onDelete();
            }}
            className="rounded border border-red-900/60 px-3 py-1 text-xs text-red-300 transition hover:bg-red-950/30"
          >
            delete node
          </button>
        </div>
      )}
    </div>
  );
}

function SuggestedCallList({
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

// AvailableInputs renders the picked tool's input shape as a list
// of opt-in rows. Required inputs are auto-prefilled when the tool
// is selected (see SuggestedCallList's onChange); this surface lists
// the optional ones (and re-adds dropped requireds) with a one-click
// "+ add" affordance per row. Rows that are already present in
// `args` are dimmed and disabled. Tools without an entry in the
// catalog (e.g. an unknown server) render nothing — the operator
// can still type free-form args via ArgsEditor below.
function AvailableInputs({
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

function ArgsEditor({
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

function StringList({
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

function BranchList({
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

// HandoffList renders the list of cross-playbook target ids on a
// terminal node. Each entry is a dropdown bound to the loaded
// playbook-id set (so typos can't sneak in for the common case);
// dangling ids — a saved playbook references a target that's since
// been renamed or deleted — surface as a red "(dangling)" option so
// the operator can fix or remove them. The "open" affordance navigates
// the editor to that playbook without leaving the page.
function HandoffList({
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
function DelegateToPicker({
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

function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <label className="block">
      <span className="mb-1 block text-xs uppercase tracking-wide text-zinc-400">
        {label}
      </span>
      {children}
    </label>
  );
}

const inputClass =
  "w-full rounded border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-sm text-zinc-100 placeholder-zinc-600 focus:border-zinc-600 focus:outline-none disabled:opacity-60";
const argInputClass =
  "w-full rounded border border-zinc-800 bg-zinc-950 px-2 py-1 text-xs text-zinc-100 placeholder-zinc-600 focus:border-zinc-600 focus:outline-none disabled:opacity-60";
