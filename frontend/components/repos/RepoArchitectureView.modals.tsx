"use client";

import { useEffect, useState } from "react";
import { useRepoSummaryStatus } from "@/components/repos/RepoSummaryStateProvider";

// EditsDiffModal renders the unified diff in a fixed overlay. Lines
// starting with `+`/`-`/`@@` get coloured; everything else stays
// neutral. Same modal-shell shape DialogProvider uses (fixed inset-0,
// z-[100], black/60 backdrop) so it stacks correctly with the
// confirm dialog if both happen to be open.
export function EditsDiffModal({
  owner,
  name,
  diff,
  onClose,
}: {
  owner: string;
  name: string;
  diff: string;
  onClose: () => void;
}) {
  // Esc + backdrop click both dismiss — matches DialogProvider's UX.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div
      className="fixed inset-0 z-[100] flex items-center justify-center bg-black/60 p-4"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="flex max-h-[85vh] w-full max-w-3xl flex-col rounded border border-zinc-700 bg-zinc-950 shadow-2xl">
        <header className="flex items-center justify-between border-b border-zinc-800 px-4 py-3">
          <div>
            <h2 className="text-sm font-semibold text-zinc-100">
              Operator edits — {owner}/{name}
            </h2>
            <p className="text-xs text-zinc-500">
              Unified diff against the AI-generated baseline
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label="close"
            className="text-zinc-500 transition hover:text-zinc-200"
          >
            ✕
          </button>
        </header>
        <pre className="overflow-auto bg-zinc-950 p-4 font-mono text-xs leading-relaxed">
          {diff.split("\n").map((line, i) => (
            <div key={i} className={diffLineClass(line)}>
              {line || " "}
            </div>
          ))}
        </pre>
      </div>
    </div>
  );
}

// diffLineClass picks a colour for a single line of unified-diff output.
// `+` adds → emerald, `-` removes → red, `@@` hunk header → sky, file
// metadata (`---`, `+++`) and `\ No newline at end of file` annotations
// → muted, context → zinc default. Pure-CSS styling — no parser needed;
// same line-prefix discipline git uses.
export function diffLineClass(line: string): string {
  if (line.startsWith("+++") || line.startsWith("---")) {
    return "text-zinc-500";
  }
  if (line.startsWith("\\")) {
    // git's "\ No newline at end of file" annotation. Mute it so it
    // reads as a footnote, not as content.
    return "text-zinc-600";
  }
  if (line.startsWith("@@")) {
    return "text-sky-400";
  }
  if (line.startsWith("+")) {
    return "text-emerald-400";
  }
  if (line.startsWith("-")) {
    return "text-red-400";
  }
  return "text-zinc-300";
}

// RefreshWithEditsConfirm presents three options when the operator
// triggers a refresh over a hand-edited summary: pass the edits as
// context (agent decides per-hunk what to keep), regenerate fresh
// (drop the edits), or cancel. A vanilla useDialog().confirm only
// offers two paths so we render this inline with the same modal
// shell the rest of the app uses.
export function RefreshWithEditsConfirm({
  owner,
  name,
  diff,
  onCancel,
  onPassAsContext,
  onRegenerateFresh,
}: {
  owner: string;
  name: string;
  diff?: string;
  onCancel: () => void;
  onPassAsContext: () => void;
  onRegenerateFresh: () => void;
}) {
  const [showDiff, setShowDiff] = useState(false);

  // Esc dismisses the confirm — but only when the diff sub-modal is
  // closed. When showDiff is true the inner EditsDiffModal owns Esc;
  // without this guard, both handlers would fire and Esc-from-diff
  // would dismiss both modals when the operator only meant to back
  // out of the diff view.
  useEffect(() => {
    if (showDiff) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCancel();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onCancel, showDiff]);

  if (showDiff && diff) {
    return (
      <EditsDiffModal
        owner={owner}
        name={name}
        diff={diff}
        onClose={() => setShowDiff(false)}
      />
    );
  }

  return (
    <div
      className="fixed inset-0 z-[100] flex items-center justify-center bg-black/60 p-4"
      onClick={(e) => {
        if (e.target === e.currentTarget) onCancel();
      }}
    >
      <div className="w-full max-w-md rounded border border-zinc-700 bg-zinc-950 p-5 shadow-2xl">
        <h2 className="text-base font-semibold text-zinc-100">
          You have manual edits
        </h2>
        <p className="mt-2 text-sm text-zinc-400">
          The current summary for{" "}
          <code className="rounded bg-zinc-800 px-1 py-0.5 font-mono text-xs text-zinc-200">
            {owner}/{name}
          </code>{" "}
          has been hand-edited since the last AI generation. How should the
          regeneration handle them?
        </p>
        {diff && (
          <button
            type="button"
            onClick={() => setShowDiff(true)}
            className="mt-3 text-xs text-sky-400 transition hover:text-sky-300 hover:underline"
          >
            view edits diff →
          </button>
        )}
        <footer className="mt-5 flex flex-col gap-2">
          <button
            type="button"
            autoFocus
            onClick={onPassAsContext}
            className="rounded bg-zinc-100 px-3 py-2 text-xs font-medium text-zinc-900 transition hover:bg-white"
          >
            pass edits to the agent as context
          </button>
          <button
            type="button"
            onClick={onRegenerateFresh}
            className="rounded border border-zinc-700 px-3 py-2 text-xs text-zinc-200 transition hover:border-zinc-500"
          >
            regenerate fresh — discard my edits
          </button>
          <button
            type="button"
            onClick={onCancel}
            className="text-xs text-zinc-500 transition hover:text-zinc-300"
          >
            cancel
          </button>
        </footer>
      </div>
    </div>
  );
}

export function StatusCard({
  status,
  loadErr,
}: {
  status: ReturnType<typeof useRepoSummaryStatus>;
  loadErr: string | null;
}) {
  if (loadErr) {
    return (
      <div className="rounded border border-red-900/60 bg-red-950/40 p-3 text-xs text-red-200/90">
        could not load summary: {loadErr}
      </div>
    );
  }
  if (!status) {
    return (
      <div className="rounded border border-zinc-800 bg-zinc-900/40 p-3 text-xs text-zinc-500">
        loading status…
      </div>
    );
  }
  if (status.inFlight) {
    return (
      <div className="rounded border border-zinc-800 bg-zinc-900/40 p-3 text-xs text-zinc-300">
        ⏳ generating architecture summary — safe to close this window;
        we'll alert you when it's done.
      </div>
    );
  }
  if (status.error) {
    return (
      <div className="rounded border border-red-900/60 bg-red-950/40 p-3 text-xs text-red-200/90">
        ⨯ generation failed: {status.error}
      </div>
    );
  }
  if (!status.exists) {
    return (
      <div className="rounded border border-zinc-800 bg-zinc-900/40 p-3 text-xs text-zinc-500">
        no cached summary — click refresh to generate one.
      </div>
    );
  }
  const generated = status.generatedAt
    ? new Date(status.generatedAt).toLocaleString()
    : "unknown";
  const kb = status.byteCount
    ? ` · ${(status.byteCount / 1024).toFixed(1)} KB`
    : "";
  return (
    <div className="rounded border border-zinc-800 bg-zinc-900/40 p-3 text-xs text-zinc-300">
      ✓ generated {generated} · {status.kind ?? "freeform"}
      {kb}
    </div>
  );
}
