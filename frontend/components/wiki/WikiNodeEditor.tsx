"use client";

// WikiNodeEditor is the right-side aside in WikiEditor — a context-
// sensitive form bound to whichever graph node is currently selected.
// Stateless: takes the active draft + load state and dispatches edits
// upward. Mirrors NodeEditor.tsx in shape; the inputs are different
// because wiki nodes have different schemas than playbook nodes.

import type { WikiBacklink } from "@/lib/wiki-api";
import { ChevronRightIcon } from "@/components/Icons";

// IncidentDraft mirrors the shape held in WikiEditor's draft store.
// Kept as a duplicate type here (rather than imported from the editor)
// so this component stays usable in isolation; the fields are the few
// the operator can edit (title + severity + body).
export type IncidentDraftView = {
  kind: "incident";
  id: string;
  title: string;
  severity: string; // "" | "sev1" | "sev2" | "sev3"
  // Read-only frontmatter fields surfaced as badges so the operator
  // sees what's preserved verbatim across an edit.
  status?: string;
  date?: string;
  services?: string[];
  errors?: string[];
  symptoms?: string[];
  body: string;
};

export type EntityDraftView = {
  kind: "entity";
  type: string;
  name: string;
  created?: string;
  body: string;
  backlinks?: WikiBacklink[];
};

type Props = {
  // null when nothing is selected (rare — the editor seeds the root
  // selection on mount). Renders a hint pointing the operator at the
  // diagram in that case.
  view: IncidentDraftView | EntityDraftView | null;
  dirty: boolean;
  // Tells the editor to switch the main pane to the body tab.
  onOpenBody: () => void;
  // Field-level patch dispatcher for incidents. The editor merges the
  // patch into the corresponding draft slot.
  onIncidentChange: (patch: { title?: string; severity?: string }) => void;
  // Whether the file backing this view exists on disk (false for an
  // unexpanded entity placeholder reference). Disables the open-body
  // button so the operator doesn't open an empty editor.
  bodyAvailable: boolean;
};

export function WikiNodeEditor({
  view,
  dirty,
  onOpenBody,
  onIncidentChange,
  bodyAvailable,
}: Props) {
  if (!view) {
    return (
      <div className="rounded border border-zinc-800 bg-zinc-900/30 p-4 text-sm text-zinc-500">
        Select a node in the diagram to edit its properties.
      </div>
    );
  }

  if (view.kind === "incident") {
    return (
      <div className="space-y-4">
        <Header
          title={view.id}
          subtitle="incident"
          dirty={dirty}
        />
        <Field label="title">
          <input
            value={view.title}
            onChange={(e) => onIncidentChange({ title: e.target.value })}
            placeholder="incident title"
            className={inputClass}
          />
        </Field>
        <Field label="severity">
          <select
            value={view.severity}
            onChange={(e) => onIncidentChange({ severity: e.target.value })}
            className={inputClass}
          >
            <option value="">—</option>
            <option value="sev1">sev1</option>
            <option value="sev2">sev2</option>
            <option value="sev3">sev3</option>
          </select>
        </Field>
        <Field label="status (read-only — use resolve button)">
          <span className="inline-block rounded bg-zinc-800 px-2 py-1 text-xs text-zinc-300">
            {view.status || "—"}
          </span>
        </Field>
        <Field label="date">
          <span className="text-xs text-zinc-500">{view.date || "—"}</span>
        </Field>
        <Field label="entity links (read-only in this iteration)">
          <div className="space-y-1.5">
            <ChipGroup label="services" values={view.services} tone="sky" />
            <ChipGroup label="errors" values={view.errors} tone="rose" />
            <ChipGroup label="symptoms" values={view.symptoms} tone="amber" />
          </div>
        </Field>

        <BodySnippetCard
          label="post-mortem"
          body={view.body}
          available={bodyAvailable}
          onOpen={onOpenBody}
        />
      </div>
    );
  }

  // entity
  return (
    <div className="space-y-4">
      <Header
        title={view.name}
        subtitle={view.type}
        dirty={dirty}
      />
      <Field label="type">
        <span className="inline-block rounded bg-zinc-800 px-2 py-1 font-mono text-xs text-zinc-300">
          {view.type}
        </span>
      </Field>
      <Field label="created">
        <span className="text-xs text-zinc-500">{view.created || "—"}</span>
      </Field>

      <BodySnippetCard
        label="notes"
        body={view.body}
        available={bodyAvailable}
        onOpen={onOpenBody}
      />

      {view.backlinks && view.backlinks.length > 0 && (
        <Field
          label={`referenced in ${view.backlinks.length} incident${view.backlinks.length !== 1 ? "s" : ""}`}
        >
          <ul className="space-y-1 text-xs">
            {view.backlinks.slice(0, 5).map((b) => (
              <li key={b.id} className="truncate text-zinc-400">
                <span className="font-mono text-zinc-500">{b.id}</span>
                {" — "}
                {b.title}
              </li>
            ))}
            {view.backlinks.length > 5 && (
              <li className="text-zinc-600">
                + {view.backlinks.length - 5} more
              </li>
            )}
          </ul>
        </Field>
      )}
    </div>
  );
}

// ── Bits ─────────────────────────────────────────────────────────────────────

function Header({
  title,
  subtitle,
  dirty,
}: {
  title: string;
  subtitle: string;
  dirty: boolean;
}) {
  return (
    <div className="space-y-0.5 border-b border-zinc-800 pb-2">
      <div className="flex items-baseline justify-between gap-2">
        <span className="font-mono text-sm text-zinc-100">{title}</span>
        {dirty && (
          <span
            title="unsaved edits in this node"
            className="inline-flex items-center gap-1 rounded-full bg-amber-950/60 px-1.5 py-0.5 text-[10px] font-medium uppercase text-amber-300"
          >
            <span aria-hidden className="h-1.5 w-1.5 rounded-full bg-amber-400" />
            unsaved
          </span>
        )}
      </div>
      <div className="text-[10px] uppercase tracking-wide text-zinc-500">
        {subtitle}
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
    <label className="block space-y-1">
      <span className="block text-[10px] font-medium uppercase tracking-wide text-zinc-500">
        {label}
      </span>
      {children}
    </label>
  );
}

function ChipGroup({
  label,
  values,
  tone,
}: {
  label: string;
  values?: string[];
  tone: "sky" | "rose" | "amber";
}) {
  if (!values || values.length === 0) return null;
  const cls = {
    sky: "bg-sky-900/40 text-sky-300",
    rose: "bg-rose-900/40 text-rose-300",
    amber: "bg-amber-900/40 text-amber-300",
  }[tone];
  return (
    <div className="flex flex-wrap items-baseline gap-1">
      <span className="text-[10px] uppercase text-zinc-600">{label}</span>
      {values.map((v) => (
        <span
          key={v}
          className={`rounded px-1.5 py-0.5 font-mono text-[10px] ${cls}`}
        >
          {v}
        </span>
      ))}
    </div>
  );
}

function BodySnippetCard({
  label,
  body,
  available,
  onOpen,
}: {
  label: string;
  body: string;
  available: boolean;
  onOpen: () => void;
}) {
  const trimmed = body.trim();
  const snippet =
    trimmed.length > 200 ? trimmed.slice(0, 200) + "…" : trimmed || "(empty)";
  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between">
        <span className="block text-[10px] font-medium uppercase tracking-wide text-zinc-500">
          {label}
        </span>
        <button
          type="button"
          onClick={onOpen}
          disabled={!available}
          title={
            available
              ? `open ${label} editor`
              : "no file on disk yet for this node"
          }
          className="inline-flex items-center gap-1 rounded border border-zinc-700 px-2 py-0.5 text-[10px] text-zinc-300 transition hover:border-zinc-500 hover:text-zinc-100 disabled:cursor-not-allowed disabled:border-zinc-800 disabled:text-zinc-600"
        >
          open
          <ChevronRightIcon className="h-3 w-3" />
        </button>
      </div>
      <pre className="line-clamp-6 whitespace-pre-wrap rounded border border-zinc-800 bg-zinc-950/40 px-2 py-1.5 font-mono text-[10px] text-zinc-400">
        {snippet}
      </pre>
    </div>
  );
}

const inputClass =
  "w-full rounded border border-zinc-700 bg-zinc-950 px-2 py-1 text-sm text-zinc-100 focus:border-zinc-500 focus:outline-none";
