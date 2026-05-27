"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { api, ApiError } from "@/lib/api";
import { parsePlaybookYAML, validate } from "@/lib/playbook";

type Props = {
  // Existing ids the modal validates "from raw YAML" against — pasting
  // YAML whose `id:` collides with an existing playbook is rejected
  // up-front rather than failing on save.
  existingIds: Set<string>;
  // Default type slot for the new playbook. The YAML body doesn't
  // carry type (directory-as-type), so this drives the savePlaybook
  // type arg for the paste path. Blank path uses the editor's own
  // type picker after open.
  defaultType: string;
  // Called with no args for the blank-template path; the parent
  // routes to the editor's __new flow.
  onChooseBlank: () => void;
  // Called with the saved id after a successful paste-and-save.
  // Parent routes to the editor for that id and bumps its refresh
  // counter so the sidebar/list pick up the new entry.
  onSavedFromYAML: (id: string) => void;
  onClose: () => void;
};

type Mode = "blank" | "paste";

// NewPlaybookModal opens when the operator clicks the sidebar's
// "+ new playbook" button. Two paths:
//
//   - blank: drops them into the editor's __new flow with an empty
//     template. Same as the previous direct-route behaviour.
//   - paste: textarea for raw YAML with live client-side validation.
//     On submit, server-validates + writes the file via savePlaybook,
//     then the parent routes to the editor for follow-up edits.
//
// The paste path is meant for "I have YAML from somewhere — let me
// drop it in and start editing." Common cases: porting a playbook
// from a different launcher install, recovering a draft from a chat
// transcript, hand-writing an outline before the visual editor.
export function NewPlaybookModal({
  existingIds,
  defaultType,
  onChooseBlank,
  onSavedFromYAML,
  onClose,
}: Props) {
  const [mode, setMode] = useState<Mode>("blank");
  const [yaml, setYaml] = useState("");
  const [type, setType] = useState(defaultType || "investigation");
  const [submitting, setSubmitting] = useState(false);
  const [serverErrs, setServerErrs] = useState<string[]>([]);
  const [serverError, setServerError] = useState<string | null>(null);

  // Esc-to-close, focus the first field when paste opens.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  const yamlRef = useRef<HTMLTextAreaElement | null>(null);
  useEffect(() => {
    if (mode === "paste") {
      // Defer one tick so the textarea has mounted.
      requestAnimationFrame(() => yamlRef.current?.focus());
    }
  }, [mode]);

  // Live client-side validation on the paste textarea. Catches
  // YAML-shape errors (parse failures), structural validator errors
  // (missing entrypoint, unreachable nodes, …), and id collisions
  // before the operator hits Save. The server re-validates on save
  // so this is purely UX — but it dramatically tightens the loop:
  // operator sees the issue inline as they type, doesn't pay a
  // round-trip per keystroke.
  const yamlIssues = useMemo(() => {
    if (mode !== "paste") return null;
    const trimmed = yaml.trim();
    if (trimmed === "") return null; // empty is "not yet validated", not "invalid"
    const issues: string[] = [];
    let parsedID = "";
    try {
      const parsed = parsePlaybookYAML(yaml);
      parsedID = parsed.id ?? "";
      issues.push(...validate(parsed));
    } catch (e) {
      issues.push(`parse: ${e instanceof Error ? e.message : String(e)}`);
    }
    if (parsedID === "") {
      // Already covered by validate(), but surface a tighter message
      // in the modal so the operator doesn't dig through "missing id"
      // alongside their other issues.
      if (!issues.some((x) => x.toLowerCase().includes("missing id"))) {
        issues.push("YAML must declare a top-level `id` field");
      }
    } else if (existingIds.has(parsedID)) {
      issues.push(
        `id "${parsedID}" already exists — change the YAML's id field, or open the existing playbook and use copy instead`,
      );
    }
    return issues;
  }, [mode, yaml, existingIds]);

  async function submitPaste() {
    if (!yamlIssues || yamlIssues.length > 0) return;
    setSubmitting(true);
    setServerErrs([]);
    setServerError(null);
    try {
      // Re-parse to extract the id — yamlIssues has already
      // validated, so this won't throw.
      const parsed = parsePlaybookYAML(yaml);
      const id = parsed.id;
      const errs = await api.savePlaybook(id, yaml, type, true);
      if (errs && errs.length > 0) {
        setServerErrs(errs);
        return;
      }
      onSavedFromYAML(id);
    } catch (e) {
      setServerError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center bg-zinc-950/80 px-4 py-12"
      role="dialog"
      aria-modal="true"
      aria-labelledby="new-playbook-title"
      onClick={(e) => {
        // Backdrop click closes; click inside the panel doesn't.
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="w-full max-w-2xl rounded-lg border border-zinc-800 bg-zinc-950 shadow-2xl">
        <header className="flex items-baseline justify-between gap-3 border-b border-zinc-800 px-5 py-3">
          <h2
            id="new-playbook-title"
            className="text-base font-semibold text-zinc-100"
          >
            New playbook
          </h2>
          <button
            type="button"
            onClick={onClose}
            aria-label="close"
            className="rounded p-1 text-zinc-500 transition hover:bg-zinc-900 hover:text-zinc-200"
          >
            ✕
          </button>
        </header>

        <div className="space-y-4 px-5 py-4">
          {/* Two-tab radio between paths. Side-by-side to keep the
              decision visible at a glance — neither path is hidden
              behind a dropdown. */}
          <div className="grid grid-cols-2 gap-2">
            <ModeCard
              active={mode === "blank"}
              onClick={() => setMode("blank")}
              title="blank template"
              subtitle="Start from a one-node skeleton and build up in the editor."
            />
            <ModeCard
              active={mode === "paste"}
              onClick={() => setMode("paste")}
              title="paste raw YAML"
              subtitle="Drop in YAML from somewhere — port, restore, or hand-author. Validated as you type."
            />
          </div>

          {mode === "blank" ? (
            <div className="text-xs text-zinc-400">
              Opens the editor with a single seed node. Fill in the id,
              symptom, type, and decision tree.
            </div>
          ) : (
            <div className="space-y-2">
              <label className="flex items-center gap-2 text-xs text-zinc-400">
                <span className="w-12 text-right uppercase tracking-wide">
                  type
                </span>
                <input
                  value={type}
                  onChange={(e) => setType(e.target.value)}
                  placeholder="investigation"
                  className="flex-1 rounded border border-zinc-800 bg-zinc-950 px-2 py-1 font-mono text-xs text-zinc-200 focus:border-zinc-600 focus:outline-none"
                />
                <span className="text-xs text-zinc-600">
                  type slot the file lives under (directory-as-type)
                </span>
              </label>
              <textarea
                ref={yamlRef}
                value={yaml}
                onChange={(e) => setYaml(e.target.value)}
                placeholder={
                  "id: my_new_playbook\nschema_version: 1\nsymptom: \"…\"\nentrypoint: start\nnodes:\n  start:\n    description: \"…\"\n    next:\n      - condition: \"…\"\n        goto: terminal_done\n  terminal_done:\n    description: \"Done.\"\n    terminal_advice: \"…\"\n"
                }
                rows={14}
                spellCheck={false}
                className="w-full resize-y rounded border border-zinc-800 bg-zinc-950 px-3 py-2 font-mono text-xs text-zinc-100 placeholder-zinc-700 focus:border-zinc-600 focus:outline-none"
              />
              <YAMLIssues issues={yamlIssues} />
              {serverErrs.length > 0 && (
                <div className="rounded border border-red-900/60 bg-red-950/30 p-2 text-xs text-red-200/90">
                  <div className="mb-1 font-medium">
                    server validation rejected the playbook:
                  </div>
                  <ul className="list-disc pl-5">
                    {serverErrs.map((e, i) => (
                      <li key={i}>{e}</li>
                    ))}
                  </ul>
                </div>
              )}
              {serverError && (
                <div className="rounded border border-red-900/60 bg-red-950/30 p-2 text-xs text-red-200/90">
                  {serverError}
                </div>
              )}
            </div>
          )}
        </div>

        <footer className="flex items-center justify-end gap-2 border-t border-zinc-800 px-5 py-3">
          <button
            type="button"
            onClick={onClose}
            className="rounded border border-zinc-800 px-3 py-1.5 text-xs text-zinc-400 transition hover:text-zinc-200"
          >
            cancel
          </button>
          {mode === "blank" ? (
            <button
              type="button"
              onClick={onChooseBlank}
              className="rounded bg-zinc-100 px-3 py-1.5 text-xs font-medium text-zinc-900 transition hover:bg-white"
            >
              start blank
            </button>
          ) : (
            <button
              type="button"
              onClick={submitPaste}
              disabled={
                submitting ||
                yaml.trim() === "" ||
                (yamlIssues?.length ?? 0) > 0
              }
              className="rounded bg-zinc-100 px-3 py-1.5 text-xs font-medium text-zinc-900 transition hover:bg-white disabled:cursor-not-allowed disabled:bg-zinc-700 disabled:text-zinc-400"
            >
              {submitting ? "saving…" : "save & open editor"}
            </button>
          )}
        </footer>
      </div>
    </div>
  );
}

function ModeCard({
  active,
  onClick,
  title,
  subtitle,
}: {
  active: boolean;
  onClick: () => void;
  title: string;
  subtitle: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={
        "flex flex-col items-start gap-1 rounded border px-3 py-2 text-left transition " +
        (active
          ? "border-sky-700 bg-sky-950/30 text-zinc-100"
          : "border-zinc-800 bg-zinc-900/30 text-zinc-300 hover:border-zinc-700 hover:bg-zinc-900")
      }
    >
      <span className="text-sm font-medium">{title}</span>
      <span className="text-xs text-zinc-500">{subtitle}</span>
    </button>
  );
}

function YAMLIssues({ issues }: { issues: string[] | null }) {
  if (issues === null) {
    return (
      <p className="text-xs text-zinc-500">
        Paste your YAML above. Issues show up here as you type.
      </p>
    );
  }
  if (issues.length === 0) {
    return (
      <div className="flex items-center gap-1.5 rounded border border-emerald-900/60 bg-emerald-950/30 px-2 py-1 text-xs text-emerald-200">
        <span aria-hidden>✓</span> looks valid
      </div>
    );
  }
  return (
    <div className="rounded border border-amber-900/60 bg-amber-950/30 p-2 text-xs text-amber-200">
      <div className="mb-1 font-medium">
        {issues.length} issue{issues.length === 1 ? "" : "s"}:
      </div>
      <ul className="list-disc pl-5">
        {issues.map((e, i) => (
          <li key={i}>{e}</li>
        ))}
      </ul>
    </div>
  );
}
