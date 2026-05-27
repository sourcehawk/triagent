"use client";

import { useState } from "react";
import { api, ApiError } from "@/lib/api";
import { Spinner } from "./Spinner";

type Mode = "local" | "pr";

type State =
  | { kind: "draft" }
  | { kind: "submitting" }
  | {
      kind: "deleted";
      mode: Mode;
      prUrl?: string;
      pushWarning?: string;
    }
  | { kind: "error"; message: string };

type Props = {
  name: string;
  onClose: () => void;
  // Called after a successful delete (either path) so the parent can
  // refetch the type list + playbook list. The modal's success view
  // is shown until the operator clicks "done"; this hook fires earlier
  // so the homepage tabs update without waiting on the operator.
  onDeleted: () => void | Promise<void>;
};

// DeleteTypeModal is the UI for removing an empty playbook type that
// also exists at origin/HEAD. The operator picks between a local-only
// delete (fast, but reverted on next sync) and a PR-based removal
// (durable). Operator-only types skip this modal entirely — the
// PlaybookList opens a plain dialog.confirm for those instead.
export function DeleteTypeModal({ name, onClose, onDeleted }: Props) {
  const [mode, setMode] = useState<Mode>("pr");
  const [state, setState] = useState<State>({ kind: "draft" });

  async function submit() {
    setState({ kind: "submitting" });
    try {
      if (mode === "local") {
        await api.deletePlaybookType(name);
        setState({ kind: "deleted", mode: "local" });
      } else {
        const res = await api.proposePlaybookTypeRemoval(name);
        setState({
          kind: "deleted",
          mode: "pr",
          prUrl: res.prUrl,
          pushWarning: res.pushWarning,
        });
      }
      void onDeleted();
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
        if (e.target === e.currentTarget && state.kind !== "submitting") onClose();
      }}
    >
      <div className="w-full max-w-md rounded-lg border border-zinc-800 bg-zinc-950 p-5 shadow-xl">
        <header className="mb-3 flex items-baseline justify-between">
          <h3 className="text-sm font-semibold text-zinc-100">
            Delete empty type{" "}
            <span className="font-mono text-zinc-300">{name}</span>
          </h3>
          <button
            type="button"
            disabled={state.kind === "submitting"}
            onClick={() => {
              if (state.kind !== "submitting") onClose();
            }}
            className="text-xs text-zinc-500 hover:text-zinc-200 disabled:cursor-not-allowed disabled:text-zinc-700"
          >
            close
          </button>
        </header>

        {state.kind === "deleted" ? (
          <div className="space-y-3">
            <div className="rounded border border-emerald-900/60 bg-emerald-950/30 px-3 py-2 text-xs text-emerald-200">
              Type <span className="font-mono">{name}</span>{" "}
              {state.mode === "local"
                ? "removed locally. Will return on next sync if it exists upstream."
                : state.pushWarning
                  ? "removed locally. Upstream removal was not pushed (see warning below)."
                  : "removed locally; PR opened for upstream removal."}
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
                Local working tree is on the removal branch, but the
                upstream PR push didn't go through:{" "}
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
              if (state.kind !== "submitting") void submit();
            }}
            className="space-y-3"
          >
            <p className="text-xs text-zinc-400">
              This type exists in the upstream repo. A local delete is
              reverted on the next sync.
            </p>
            <div className="space-y-2">
              <label className="flex cursor-pointer items-start gap-2 rounded border border-zinc-800 bg-zinc-900/40 px-3 py-2 text-xs transition hover:border-zinc-600">
                <input
                  type="radio"
                  name="delete-mode"
                  checked={mode === "pr"}
                  onChange={() => setMode("pr")}
                  className="mt-0.5"
                />
                <span>
                  <span className="block font-medium text-zinc-100">
                    Propose removal as PR
                    <span className="ml-1 text-zinc-500">(recommended)</span>
                  </span>
                  <span className="mt-0.5 block text-zinc-500">
                    Opens a PR deleting{" "}
                    <span className="font-mono">{name}/type.txt</span>{" "}
                    upstream.
                  </span>
                </span>
              </label>
              <label className="flex cursor-pointer items-start gap-2 rounded border border-zinc-800 bg-zinc-900/40 px-3 py-2 text-xs transition hover:border-zinc-600">
                <input
                  type="radio"
                  name="delete-mode"
                  checked={mode === "local"}
                  onChange={() => setMode("local")}
                  className="mt-0.5"
                />
                <span>
                  <span className="block font-medium text-zinc-100">
                    Local only
                  </span>
                  <span className="mt-0.5 block text-zinc-500">
                    Gone until next sync.
                  </span>
                </span>
              </label>
            </div>
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
                disabled={state.kind === "submitting"}
                className="inline-flex items-center gap-1.5 rounded bg-red-600 px-3 py-1.5 text-xs font-medium text-white transition hover:bg-red-500 disabled:cursor-not-allowed disabled:bg-red-900 disabled:text-red-300"
              >
                {state.kind === "submitting" && (
                  <Spinner className="h-3 w-3" />
                )}
                delete
              </button>
            </div>
          </form>
        )}
      </div>
    </div>
  );
}
