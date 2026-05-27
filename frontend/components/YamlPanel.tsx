"use client";

import { CheckIcon } from "./Icons";
import { CopyButton } from "./CopyButton";

type Props = {
  yaml: string;
  errors: string[];
  // The set of "structural" errors the client-side validator surfaces;
  // server-side validation errors (returned from POST /api/playbooks/{id})
  // are passed through the same `errors` array — we don't bother to
  // distinguish since the user just needs to see "fix these things".
  collapsed: boolean;
  onToggleCollapsed: () => void;
  // embedded: hide the panel's own header chrome (the tab bar above
  // it serves the same role), let the YAML body fill the full
  // available height. Used by the editor's tabbed layout.
  embedded?: boolean;
};

export function YamlPanel({
  yaml,
  errors,
  collapsed,
  onToggleCollapsed,
  embedded = false,
}: Props) {
  return (
    <section
      className={
        "flex flex-col rounded border border-zinc-800 bg-zinc-900/30 " +
        (embedded ? "h-full" : "")
      }
    >
      {!embedded && (
      <header className="flex items-center justify-between border-b border-zinc-800 px-3 py-2">
        <button
          type="button"
          onClick={onToggleCollapsed}
          className="flex items-center gap-2 text-xs text-zinc-300 hover:text-zinc-100"
        >
          <span aria-hidden className="text-zinc-500">
            {collapsed ? "▸" : "▾"}
          </span>
          YAML preview
          {errors.length > 0 ? (
            <span className="rounded-full bg-red-900/60 px-1.5 py-0.5 text-xs font-medium text-red-200">
              {errors.length} issue{errors.length === 1 ? "" : "s"}
            </span>
          ) : yaml.trim().length > 0 ? (
            <span
              title="YAML is structurally valid"
              className="inline-flex items-center gap-1 rounded-full bg-emerald-900/60 px-1.5 py-0.5 text-xs font-medium text-emerald-200"
            >
              <CheckIcon className="h-3 w-3" />
              valid
            </span>
          ) : null}
        </button>
        <CopyButton text={yaml} label="copy" />
      </header>
      )}

      {(embedded || !collapsed) && (
        <>
          {embedded && (
            // Embedded mode keeps the copy button reachable since the
            // chrome header is hidden. Right-aligned so the YAML body
            // starts flush with the tab content.
            <div className="flex items-center justify-end border-b border-zinc-800 bg-zinc-900/40 px-3 py-1">
              <CopyButton text={yaml} label="copy" />
            </div>
          )}
          {errors.length > 0 && (
            <div className="space-y-0.5 border-b border-red-900/60 bg-red-950/30 px-3 py-2 text-xs text-red-200/90">
              {errors.map((e, i) => (
                <div key={i}>• {e}</div>
              ))}
            </div>
          )}
          <pre
            className={
              "overflow-auto whitespace-pre-wrap break-words bg-zinc-950 p-3 font-mono text-xs leading-relaxed text-zinc-200 " +
              (embedded ? "min-h-0 flex-1" : "max-h-72")
            }
          >
            {yaml}
          </pre>
        </>
      )}
    </section>
  );
}
