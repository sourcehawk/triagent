"use client";

import { useEffect, useRef, useState } from "react";
import { Spinner } from "@/components/shared/Spinner";

type Props = {
  playbookID: string;
  dirty: boolean;
  defaultMessage?: string;
  onConfirm: (message: string) => void | Promise<void>;
  onCancel: () => void;
  saving: boolean;
  errors: string[];
};

// SaveDialog collects an optional commit message before saving a playbook.
// Follows the same modal pattern as PushPRModal: backdrop click / Escape
// to close (when not saving), autofocus the message input.
export function SaveDialog({
  playbookID,
  dirty,
  defaultMessage = "",
  onConfirm,
  onCancel,
  saving,
  errors,
}: Props) {
  const [message, setMessage] = useState(defaultMessage);
  const inputRef = useRef<HTMLInputElement>(null);

  // Autofocus the message input when the dialog opens.
  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  // Close on Escape (when not saving).
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape" && !saving) onCancel();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [saving, onCancel]);

  function handleConfirm() {
    void onConfirm(message);
  }

  return (
    <div
      className="fixed inset-0 z-[60] flex items-center justify-center bg-black/60 p-4"
      onClick={(e) => {
        if (e.target === e.currentTarget && !saving) onCancel();
      }}
    >
      <div className="w-full max-w-md rounded border border-zinc-700 bg-zinc-950 p-5 shadow-2xl">
        <header className="mb-4 space-y-1">
          <h2 className="text-base font-semibold text-zinc-100">
            Save changes to{" "}
            <code className="rounded bg-zinc-800 px-1 py-0.5 font-mono text-sm text-zinc-300">
              {playbookID}
            </code>
          </h2>
          {!dirty && (
            <p className="text-xs text-zinc-500">No unsaved changes.</p>
          )}
        </header>

        <div className="space-y-3">
          <label className="block">
            <span className="mb-1 block text-xs uppercase tracking-wide text-zinc-400">
              commit message{" "}
              <span className="normal-case text-zinc-600">(optional)</span>
            </span>
            <input
              ref={inputRef}
              type="text"
              value={message}
              onChange={(e) => setMessage(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && dirty && !saving) handleConfirm();
              }}
              placeholder={`${playbookID}: update`}
              disabled={saving || !dirty}
              maxLength={120}
              className="w-full rounded border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-sm text-zinc-100 placeholder-zinc-600 focus:border-zinc-600 focus:outline-none disabled:opacity-60"
            />
          </label>

          {errors.length > 0 && (
            <div className="rounded border border-red-900/60 bg-red-950/40 p-2 text-xs text-red-200/90">
              <div className="mb-1 font-medium">Save failed:</div>
              <ul className="space-y-0.5">
                {errors.map((e, i) => (
                  <li key={i}>• {e}</li>
                ))}
              </ul>
            </div>
          )}
        </div>

        <footer className="mt-5 flex items-center justify-end gap-2">
          <button
            type="button"
            onClick={onCancel}
            disabled={saving}
            className="rounded border border-zinc-800 px-3 py-1.5 text-xs text-zinc-400 transition hover:text-zinc-200 disabled:opacity-50"
          >
            cancel
          </button>
          <button
            type="button"
            data-testid="triagent-playbook-save-confirm"
            onClick={handleConfirm}
            disabled={saving || !dirty}
            className="flex items-center gap-2 rounded bg-zinc-100 px-3 py-1.5 text-xs font-medium text-zinc-900 transition hover:bg-white disabled:cursor-not-allowed disabled:bg-zinc-700 disabled:text-zinc-400"
          >
            {saving && (
              <Spinner className="h-3 w-3 border-zinc-500 border-t-zinc-900" />
            )}
            {saving ? "saving…" : "save"}
          </button>
        </footer>
      </div>
    </div>
  );
}
