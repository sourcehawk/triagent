"use client";

import { useEffect, useState } from "react";
import {
  api,
  ApiError,
  type Capabilities,
  type PushPRResult,
} from "@/lib/api";
import { Spinner } from "@/components/shared/Spinner";
import { CopyButton } from "@/components/shared/CopyButton";

type Props = {
  // Playbook id we're pushing — also seeds the default branch name.
  playbookID: string;
  // Symptom + YAML at modal-open time. yaml ships to the server; symptom
  // seeds the default PR title.
  yaml: string;
  symptom: string;
  // Type slot the file lands under upstream. Threaded straight to the
  // push-PR endpoint so the file lands at <type>/<id>.yaml.
  type: string;
  capabilities: Capabilities;
  onClose: () => void;
};

// PushPRModal collects optional branch / title / body, hits POST
// /api/playbooks/{id}/push-pr, and surfaces the result. Two terminal
// states:
//   - success: shows the PR URL with a "Open in browser" + "Copy" pair.
//   - error / validation failure: shows the messages and lets the user
//     adjust + retry without rebuilding the modal state.
export function PushPRModal({
  playbookID,
  yaml,
  symptom,
  type,
  capabilities,
  onClose,
}: Props) {
  // Empty default — the backend auto-generates a branch with a
  // timestamped suffix (playbook/<id>-<YYYYMMDD-HHMMSS>) when this is
  // blank, which is what we want every time. Pre-filling with
  // playbook/<id>-<YYYYMMDD> caused same-day re-pushes to collide on
  // the existing remote ref. The operator can still type a custom
  // branch if they want to update an existing PR (the backend
  // force-with-lease handles overwriting).
  const branchPlaceholder = `auto: playbook/${playbookID}-<timestamp>`;
  const [branch, setBranch] = useState("");
  const defaultTitle = `playbook(${playbookID}): ${truncate(symptom, 60)}`;
  const [title, setTitle] = useState(defaultTitle);
  const [body, setBody] = useState(
    `Updates the \`${playbookID}\` investigation playbook via the Triagent editor.`,
  );
  const [base, setBase] = useState("main");
  const [pushing, setPushing] = useState(false);
  const [errors, setErrors] = useState<string[] | null>(null);
  const [result, setResult] = useState<PushPRResult | null>(null);

  // Close on Escape.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape" && !pushing) onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [pushing, onClose]);

  async function push() {
    setPushing(true);
    setErrors(null);
    try {
      const res = await api.pushPlaybookPR(playbookID, {
        yaml,
        type,
        branch,
        title,
        body,
        base,
      });
      if (res.ok) {
        setResult(res.result);
      } else {
        setErrors(res.errors);
      }
    } catch (e) {
      setErrors([e instanceof ApiError ? e.message : String(e)]);
    } finally {
      setPushing(false);
    }
  }

  return (
    <div
      className="fixed inset-0 z-[60] flex items-center justify-center bg-black/60 p-4"
      onClick={(e) => {
        if (e.target === e.currentTarget && !pushing) onClose();
      }}
    >
      <div className="w-full max-w-xl rounded border border-zinc-700 bg-zinc-950 p-5 shadow-2xl">
        {result ? (
          <SuccessView result={result} onClose={onClose} />
        ) : (
          <>
            <header className="mb-4 space-y-1">
              <h2 className="text-base font-semibold text-zinc-100">
                Push playbook as PR
              </h2>
              <p className="text-xs text-zinc-500">
                {capabilities.gh.user
                  ? `Will push as ${capabilities.gh.user} from `
                  : "Will push from "}
                <code className="rounded bg-zinc-800 px-1 py-0.5 font-mono text-xs text-zinc-300 break-all">
                  {capabilities.repoPath.path}
                </code>{" "}
                → PR opens on the remote.
              </p>
            </header>

            <div className="space-y-3">
              <Field label="branch">
                <input
                  value={branch}
                  onChange={(e) => setBranch(e.target.value)}
                  placeholder={branchPlaceholder}
                  disabled={pushing}
                  className={inputClass}
                />
              </Field>
              <Field label="base">
                <input
                  value={base}
                  onChange={(e) => setBase(e.target.value)}
                  disabled={pushing}
                  className={inputClass}
                />
              </Field>
              <Field label="title">
                <input
                  value={title}
                  onChange={(e) => setTitle(e.target.value)}
                  disabled={pushing}
                  className={inputClass}
                />
              </Field>
              <Field label="body">
                <textarea
                  value={body}
                  onChange={(e) => setBody(e.target.value)}
                  disabled={pushing}
                  rows={4}
                  className={`${inputClass} resize-y`}
                />
              </Field>

              {errors && errors.length > 0 && (
                <div className="rounded border border-red-900/60 bg-red-950/40 p-2 text-xs text-red-200/90">
                  <div className="mb-1 font-medium">Push failed:</div>
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
                onClick={onClose}
                disabled={pushing}
                className="rounded border border-zinc-800 px-3 py-1.5 text-xs text-zinc-400 transition hover:text-zinc-200 disabled:opacity-50"
              >
                cancel
              </button>
              <button
                type="button"
                onClick={push}
                disabled={pushing || !title.trim() || !base.trim()}
                className="flex items-center gap-2 rounded bg-zinc-100 px-3 py-1.5 text-xs font-medium text-zinc-900 transition hover:bg-white disabled:cursor-not-allowed disabled:bg-zinc-700 disabled:text-zinc-400"
              >
                {pushing && <Spinner className="h-3 w-3 border-zinc-500 border-t-zinc-900" />}
                {pushing ? "pushing…" : "push & open PR"}
              </button>
            </footer>
          </>
        )}
      </div>
    </div>
  );
}

function SuccessView({
  result,
  onClose,
}: {
  result: PushPRResult;
  onClose: () => void;
}) {
  return (
    <>
      <header className="mb-3 space-y-1">
        <h2 className="text-base font-semibold text-emerald-300">
          Pull request opened
        </h2>
        <p className="text-xs text-zinc-500">
          Branch{" "}
          <code className="rounded bg-zinc-800 px-1 py-0.5 font-mono text-xs text-zinc-300">
            {result.branch}
          </code>{" "}
          → <code className="rounded bg-zinc-800 px-1 py-0.5 font-mono text-xs text-zinc-300">{result.base}</code>
        </p>
      </header>
      <div className="rounded border border-zinc-800 bg-zinc-900/40 p-3">
        <a
          href={result.url}
          target="_blank"
          rel="noopener noreferrer"
          className="break-all font-mono text-xs text-sky-300 underline hover:text-sky-200"
        >
          {result.url}
        </a>
      </div>
      <footer className="mt-5 flex items-center justify-end gap-2">
        <CopyButton text={result.url} label="copy URL" size="md" />
        <a
          href={result.url}
          target="_blank"
          rel="noopener noreferrer"
          className="rounded bg-zinc-100 px-3 py-1.5 text-xs font-medium text-zinc-900 transition hover:bg-white"
        >
          open in browser
        </a>
        <button
          type="button"
          onClick={onClose}
          className="rounded border border-zinc-800 px-3 py-1.5 text-xs text-zinc-400 transition hover:text-zinc-200"
        >
          close
        </button>
      </footer>
    </>
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
    <label className="block">
      <span className="mb-1 block text-xs uppercase tracking-wide text-zinc-400">
        {label}
      </span>
      {children}
    </label>
  );
}

function truncate(s: string, max: number): string {
  if (!s) return "";
  if (s.length <= max) return s;
  return s.slice(0, max - 1) + "…";
}

const inputClass =
  "w-full rounded border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-sm text-zinc-100 placeholder-zinc-600 focus:border-zinc-600 focus:outline-none disabled:opacity-60";
