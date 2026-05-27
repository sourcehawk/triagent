"use client";

// WikiBodyTab is the main-pane body editor for the currently-selected
// wiki node. Two states: rendered Markdown (default) with an "edit"
// button, or a full-height textarea (edit mode) with a "done" button.
// Edits dispatch upward into the editor's draft store; switching tabs
// or graph selection mid-edit doesn't lose anything because the
// content lives in the parent's state.

import { useState } from "react";
import { Markdown } from "@/components/Markdown";
import { CheckIcon, EditIcon } from "@/components/Icons";

type Props = {
  // Header label shown at the top of the tab content. Pass
  // "post-mortem" for incidents, "notes" for entities.
  label: string;
  // Identity breadcrumb so the operator knows which node this body
  // belongs to when they jumped between graph nodes.
  identity: string;
  body: string;
  onChange: (next: string) => void;
};

export function WikiBodyTab({ label, identity, body, onChange }: Props) {
  const [editing, setEditing] = useState(false);

  return (
    <div className="flex h-full flex-col gap-2">
      <header className="flex items-center justify-between border-b border-zinc-800 pb-2">
        <div className="flex items-baseline gap-2">
          <span className="text-[10px] uppercase tracking-wide text-zinc-500">
            {label}
          </span>
          <span className="font-mono text-sm text-zinc-200">{identity}</span>
        </div>
        <button
          type="button"
          onClick={() => setEditing((v) => !v)}
          title={editing ? "switch to render view" : "switch to edit view"}
          className={
            "inline-flex items-center gap-1.5 rounded border px-2 py-0.5 text-xs transition " +
            (editing
              ? "border-emerald-700 bg-emerald-950/40 text-emerald-200 hover:bg-emerald-900/40"
              : "border-zinc-700 text-zinc-300 hover:border-zinc-500 hover:text-zinc-100")
          }
        >
          {editing ? (
            <>
              <CheckIcon className="h-3.5 w-3.5" />
              done
            </>
          ) : (
            <>
              <EditIcon className="h-3.5 w-3.5" />
              edit
            </>
          )}
        </button>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto rounded border border-zinc-800 bg-zinc-900/30">
        {editing ? (
          <textarea
            value={body}
            onChange={(e) => onChange(e.target.value)}
            spellCheck
            className="block h-full min-h-[400px] w-full resize-none bg-transparent px-4 py-3 font-mono text-sm text-zinc-100 focus:outline-none"
          />
        ) : (
          <div className="px-5 py-4">
            <Markdown text={body || "_(empty)_"} />
          </div>
        )}
      </div>
    </div>
  );
}
