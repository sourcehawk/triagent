"use client";

import { useState } from "react";
import {
  type Investigation,
  type SessionPushPRRequest,
} from "@/lib/api";
import { ExternalLinkIcon } from "../Icons";
import { Spinner } from "../Spinner";
import type { PushState } from "../SessionView";

type Props = {
  investigation: Investigation;
  // state is owned by SessionView so the modal can close + reopen
  // without losing in-flight context. The modal is purely presentational.
  state: PushState;
  // onSubmit hands the inputs back to SessionView, which fires the
  // request and walks the state machine (pushing → success/error).
  onSubmit: (req: SessionPushPRRequest) => void;
  onClose: () => void;
};

export function PushSessionPRModal({ investigation, state, onSubmit, onClose }: Props) {
  const [branch, setBranch] = useState("");
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (state.phase === "pushing" || state.phase === "success") return;
    onSubmit({
      branch: branch || undefined,
      title: title || undefined,
      body: body || undefined,
    });
  }

  // Hide the form fields once the push starts so the operator gets a
  // clean confirmation panel. Errors keep the form visible (with the
  // failure message above the actions) so they can adjust + retry.
  const showForm = state.phase === "idle" || state.phase === "error";

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
      <form
        onSubmit={handleSubmit}
        className="w-full max-w-lg rounded border border-zinc-800 bg-zinc-950 p-5 shadow-2xl"
      >
        <h2 className="text-base font-semibold text-zinc-100">
          Push session to upstream
        </h2>
        <p className="mt-1 text-xs text-zinc-500">
          Drafts session.md with an AI sub-agent and opens a PR against{" "}
          <code>sourcehawk/triagent-sessions</code>.
        </p>

        {showForm && (
          <>
            <div className="mt-4 space-y-3">
              <Field label="Branch (optional)">
                <input
                  value={branch}
                  onChange={(e) => setBranch(e.target.value)}
                  placeholder="auto: sessions/<slug>"
                  className="w-full rounded border border-zinc-800 bg-zinc-950 px-2 py-1 text-sm text-zinc-100"
                />
              </Field>
              <Field label="Title (optional)">
                <input
                  value={title}
                  onChange={(e) => setTitle(e.target.value)}
                  placeholder="auto: session: <slug> — <title>"
                  className="w-full rounded border border-zinc-800 bg-zinc-950 px-2 py-1 text-sm text-zinc-100"
                />
              </Field>
              <Field label="PR body (optional)">
                <textarea
                  value={body}
                  onChange={(e) => setBody(e.target.value)}
                  rows={3}
                  className="w-full rounded border border-zinc-800 bg-zinc-950 px-2 py-1 text-sm text-zinc-100"
                />
              </Field>
            </div>
            {state.phase === "error" && (
              <div className="mt-3 rounded border border-red-900/60 bg-red-950/40 px-3 py-2 text-xs text-red-200/90">
                {state.message}
              </div>
            )}
            <div className="mt-5 flex items-center justify-end gap-2">
              <button
                type="button"
                onClick={onClose}
                className="rounded border border-zinc-700 px-3 py-1.5 text-sm text-zinc-200 transition hover:border-zinc-500"
              >
                cancel
              </button>
              <button
                type="submit"
                className="rounded bg-zinc-100 px-3 py-1.5 text-sm font-medium text-zinc-900 transition hover:bg-white"
              >
                {state.phase === "error" ? "retry" : "open PR"}
              </button>
            </div>
          </>
        )}

        {state.phase === "pushing" && <PushingPanel onClose={onClose} />}
        {state.phase === "success" && (
          <SuccessPanel result={state.result} onClose={onClose} />
        )}
      </form>
      {/* unused investigation prop reserved for future per-id messaging */}
      <span hidden>{investigation.id}</span>
    </div>
  );
}

function PushingPanel({ onClose }: { onClose: () => void }) {
  return (
    <>
      <div className="mt-5 flex items-start gap-3 rounded border border-sky-900/60 bg-sky-950/30 p-4">
        <Spinner className="mt-0.5 h-5 w-5 shrink-0 text-sky-300" />
        <div className="space-y-1">
          <p className="text-sm font-medium text-zinc-100">
            Drafting started
          </p>
          <p className="text-xs text-zinc-400">
            An AI sub-agent is summarizing the investigation transcript. The
            PR will be opened automatically once the draft is ready — usually
            30s to a few minutes for a long session. You can close this
            dialog and keep working; a notification will appear in the
            bottom-right corner when the PR is open (or if something goes
            wrong).
          </p>
        </div>
      </div>
      <div className="mt-5 flex items-center justify-end">
        <button
          type="button"
          onClick={onClose}
          className="rounded bg-zinc-100 px-3 py-1.5 text-sm font-medium text-zinc-900 transition hover:bg-white"
        >
          close
        </button>
      </div>
    </>
  );
}

function SuccessPanel({
  result,
  onClose,
}: {
  result: { url: string; branch: string; base: string; slug: string };
  onClose: () => void;
}) {
  return (
    <>
      <div className="mt-5 flex items-start gap-3 rounded border border-emerald-900/60 bg-emerald-950/40 p-4">
        <CheckCircle className="mt-0.5 h-5 w-5 shrink-0 text-emerald-400" />
        <div className="space-y-1">
          <p className="text-sm font-medium text-zinc-100">
            Pull request opened
          </p>
          <p className="text-xs text-zinc-400">
            Branch <code>{result.branch}</code> → <code>{result.base}</code>.
            Review the draft, edit if needed, and merge when ready.
          </p>
          <a
            href={result.url}
            target="_blank"
            rel="noreferrer"
            className="mt-1 inline-flex items-center gap-1.5 text-sm text-sky-400 transition hover:text-sky-300"
          >
            {result.url}
            <ExternalLinkIcon className="h-3.5 w-3.5" />
          </a>
        </div>
      </div>
      <div className="mt-5 flex items-center justify-end">
        <button
          type="button"
          onClick={onClose}
          className="rounded bg-zinc-100 px-3 py-1.5 text-sm font-medium text-zinc-900 transition hover:bg-white"
        >
          close
        </button>
      </div>
    </>
  );
}

function CheckCircle({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden
    >
      <circle cx="12" cy="12" r="10" />
      <path d="m9 12 2 2 4-4" />
    </svg>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block space-y-1">
      <span className="text-xs text-zinc-400">{label}</span>
      {children}
    </label>
  );
}
