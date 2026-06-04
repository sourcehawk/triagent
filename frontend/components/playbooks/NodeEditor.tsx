"use client";

import type { PlaybookNode } from "@/lib/playbook";
import type { ToolEntry } from "@/lib/api";
import { useDialog } from "@/lib/dialog";
import { Field, inputClass } from "./NodeEditor.fields";
import { SuggestedCallList, StringList, BranchList } from "./NodeEditor.lists";
import { HandoffList, DelegateToPicker } from "./NodeEditor.pickers";

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
