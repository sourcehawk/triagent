"use client";

import { useState } from "react";
import { api, ApiError } from "@/lib/api";
import { Spinner } from "@/components/shared/Spinner";

type Props = {
  onClose: () => void;
  // Called after a successful create so the parent can refetch the
  // playbook list / type dropdown. The modal's own state already
  // shows the success → close arc; this is the parent hook for
  // upstream effects.
  onCreated: () => void | Promise<void>;
};

type State =
  | { kind: "draft" }
  | { kind: "submitting" }
  | { kind: "created"; prUrl?: string; pushWarning?: string }
  | { kind: "error"; message: string };

// NewTypeModal is the "+ new type" affordance from the playbooks tab
// header. The launcher writes <type>/type.txt into the upstream
// clone immediately (so the type is usable locally on next refresh)
// and tries to push it as a PR via gh. PR-push failures show as a
// warning; the type is still created locally.
export function NewTypeModal({ onClose, onCreated }: Props) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [state, setState] = useState<State>({ kind: "draft" });

  const submitDisabled =
    state.kind === "submitting" ||
    !/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/.test(name) ||
    !description.trim();

  async function submit() {
    setState({ kind: "submitting" });
    try {
      const res = await api.createPlaybookType(name, description.trim());
      setState({
        kind: "created",
        prUrl: res.prUrl,
        pushWarning: res.pushWarning,
      });
      void onCreated();
    } catch (e) {
      setState({
        kind: "error",
        message: e instanceof ApiError ? e.message : String(e),
      });
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="w-full max-w-md rounded-lg border border-zinc-800 bg-zinc-950 p-5 shadow-xl">
        <header className="mb-3 flex items-baseline justify-between">
          <h3 className="text-sm font-semibold text-zinc-100">
            New playbook type
          </h3>
          <button
            type="button"
            onClick={onClose}
            className="text-xs text-zinc-500 hover:text-zinc-200"
          >
            close
          </button>
        </header>

        {state.kind === "created" ? (
          <div className="space-y-3">
            <div className="rounded border border-emerald-900/60 bg-emerald-950/30 px-3 py-2 text-xs text-emerald-200">
              Type{" "}
              <span className="font-mono">{name}</span> created. New
              playbooks can now be filed under it.
            </div>
            {state.prUrl && (
              <div className="text-xs text-zinc-400">
                Pushed as a PR:{" "}
                <a
                  href={state.prUrl}
                  target="_blank"
                  rel="noreferrer"
                  className="font-mono text-sky-300 underline"
                >
                  {state.prUrl}
                </a>
              </div>
            )}
            {state.pushWarning && (
              <div className="rounded border border-amber-900/60 bg-amber-950/20 px-3 py-2 text-xs text-amber-200">
                Local file written, but the upstream PR push didn't go
                through:{" "}
                <span className="font-mono">{state.pushWarning}</span>
              </div>
            )}
            <div className="flex justify-end pt-1">
              <button
                type="button"
                onClick={onClose}
                className="rounded bg-zinc-100 px-3 py-1.5 text-xs font-medium text-zinc-900 transition hover:bg-white"
              >
                done
              </button>
            </div>
          </div>
        ) : (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              if (!submitDisabled) void submit();
            }}
            className="space-y-3"
          >
            <label className="block">
              <span className="mb-1 block text-xs uppercase tracking-wide text-zinc-400">
                name
              </span>
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="snake_case_id"
                className="w-full rounded border border-zinc-800 bg-zinc-900 px-2 py-1.5 font-mono text-sm text-zinc-100 placeholder-zinc-600 focus:border-zinc-600 focus:outline-none"
              />
              <span className="mt-0.5 block text-xs text-zinc-500">
                Allowed: alphanumeric, _-, up to 64 chars. Becomes the
                directory name in the upstream repo.
              </span>
            </label>
            <label className="block">
              <span className="mb-1 block text-xs uppercase tracking-wide text-zinc-400">
                description
              </span>
              <textarea
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                placeholder="What does this type slot mean? When should the agent pick it?"
                rows={4}
                className="w-full resize-y rounded border border-zinc-800 bg-zinc-900 px-2 py-1.5 text-sm text-zinc-100 placeholder-zinc-600 focus:border-zinc-600 focus:outline-none"
              />
              <span className="mt-0.5 block text-xs text-zinc-500">
                Lands in <span className="font-mono">{name || "<type>"}/type.txt</span>.
              </span>
            </label>
            {state.kind === "error" && (
              <div className="rounded border border-red-900/60 bg-red-950/40 px-2 py-1 text-xs text-red-200/90">
                {state.message}
              </div>
            )}
            <div className="flex items-center justify-end gap-2 pt-1">
              <button
                type="button"
                onClick={onClose}
                className="rounded border border-zinc-700 px-3 py-1.5 text-xs text-zinc-300 transition hover:border-zinc-500 hover:text-zinc-100"
              >
                cancel
              </button>
              <button
                type="submit"
                disabled={submitDisabled}
                className="inline-flex items-center gap-1.5 rounded bg-zinc-100 px-3 py-1.5 text-xs font-medium text-zinc-900 transition hover:bg-white disabled:cursor-not-allowed disabled:bg-zinc-700 disabled:text-zinc-400"
              >
                {state.kind === "submitting" && (
                  <Spinner className="h-3 w-3" />
                )}
                create
              </button>
            </div>
          </form>
        )}
      </div>
    </div>
  );
}
