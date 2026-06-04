"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
  api,
  ApiError,
  type RepoSummary,
  type RepoSummaryEdits,
} from "@/lib/api";
import { ArrowLeftIcon, GitHubIcon } from "@/components/shared/Icons";
import { useDialog } from "@/lib/dialog";
import {
  repoKey,
  useRepoSummaryStatus,
  useRepoSummaryStore,
} from "@/components/repos/RepoSummaryStateProvider";
import { Spinner } from "@/components/shared/Spinner";
import {
  EditsDiffModal,
  RefreshWithEditsConfirm,
  StatusCard,
} from "./RepoArchitectureView.modals";

type Props = {
  owner: string;
  name: string;
};

// RepoArchitectureView is the per-repo page rendered at
// /repos?repo=<owner>/<name>. Shows:
//   - header (owner/name + GitHub icon)
//   - status card (phase, generated-at, kind, byte count) reading
//     live SSE state via useRepoSummaryStatus
//   - action toolbar (Refresh, Edit) — Refresh disabled while in
//     flight; Edit disabled while in flight
//   - markdown viewer (prose styling) OR textarea editor
//
// The view fetches the full summary on mount; SSE events for the
// same repo invalidate the cache and trigger a re-fetch on
// success/error transitions so the markdown stays current after
// auto-refreshes the operator didn't trigger.
export function RepoArchitectureView({ owner, name }: Props) {
  const dialog = useDialog();
  const router = useRouter();
  const store = useRepoSummaryStore();
  const status = useRepoSummaryStatus(owner, name);

  const [summary, setSummary] = useState<RepoSummary | null>(null);
  const [loadErr, setLoadErr] = useState<string | null>(null);

  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<string>("");
  const [saveErr, setSaveErr] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  // Operator-edit state. Re-fetched on mount, after a save, and after
  // every regen-finish transition. hasEdits drives the inline badge
  // and the three-option refresh confirm.
  const [edits, setEdits] = useState<RepoSummaryEdits | null>(null);
  const [showEditsModal, setShowEditsModal] = useState(false);
  const [refreshConfirm, setRefreshConfirm] = useState<{ open: boolean; diff?: string }>({ open: false });

  const fetchSummary = useCallback(() => {
    setLoadErr(null);
    api
      .getRepoSummary(owner, name)
      .then((s) => setSummary(s))
      .catch((e) =>
        setLoadErr(e instanceof ApiError ? e.message : String(e)),
      );
  }, [owner, name]);

  const fetchEdits = useCallback(() => {
    api
      .getRepoSummaryEdits(owner, name)
      .then((e) => setEdits(e))
      .catch(() => {
        /* non-fatal — badge stays hidden */
      });
  }, [owner, name]);

  // Initial fetch on mount + when owner/name change.
  useEffect(() => {
    setSummary(null);
    setEditing(false);
    setDraft("");
    setSaveErr(null);
    setEdits(null);
    fetchSummary();
    fetchEdits();
    // Also seed the lightweight status so the status card renders
    // immediately. The full /summary fetch above arrives later.
    api
      .getRepoSummaryStatus(owner, name)
      .then((s) => store.upsert(repoKey(owner, name), s))
      .catch(() => {
        /* non-fatal */
      });
  }, [owner, name, fetchSummary, fetchEdits, store]);

  // Re-fetch the body when SSE flips from in-flight to a terminal
  // phase. Catches the case where a refresh triggered elsewhere
  // (sidenav toast Retry, auto-gen-on-add) finishes while the
  // operator is sitting on the page — the status card updates via
  // the hook, but the markdown body needs a fresh GET.
  //
  // Track the previous inFlight value in a ref so the transition
  // detection happens in the same render that observes the new
  // status — no double-render lag.
  const prevInFlightRef = useRef<boolean | undefined>(undefined);
  useEffect(() => {
    const prev = prevInFlightRef.current;
    const next = status?.inFlight;
    prevInFlightRef.current = next;
    if (prev === true && next === false) {
      fetchSummary();
      // Edits state changes after every regen — a fresh AI run
      // either resets the baseline (incl. clearing the diff) or
      // preserves it depending on the includeOperatorEdits choice
      // we made when triggering the refresh.
      fetchEdits();
    }
  }, [status?.inFlight, fetchSummary, fetchEdits]);

  // Issue the actual refresh API call. Shared by the no-edits path
  // and both branches of the three-option confirm.
  const triggerRefresh = useCallback(
    async (includeOperatorEdits: boolean) => {
      try {
        await api.refreshRepoSummary(owner, name, {
          kind: "freeform",
          includeOperatorEdits,
        });
        // The /summary/refresh response is the trigger; the global
        // SSE channel publishes phase=started which the provider
        // handles. Show an inline hint here in case the toast
        // surface is busy.
        dialog.notify({
          kind: "info",
          title: `Refresh started for ${owner}/${name}`,
          body:
            "This can take up to 15 minutes for large repos. Safe to close this window or navigate away — we'll alert you when it's done.",
          ttlMs: 6000,
        });
      } catch (e) {
        const msg = e instanceof ApiError ? e.message : String(e);
        setSaveErr(msg);
      }
    },
    [owner, name, dialog],
  );

  // Refresh button: when there are operator edits, ask first; when
  // there aren't, refresh directly.
  const onRefresh = useCallback(() => {
    if (status?.inFlight) return;
    if (edits?.hasEdits) {
      setRefreshConfirm({ open: true, diff: edits.diff });
      return;
    }
    void triggerRefresh(false);
  }, [status?.inFlight, edits, triggerRefresh]);

  const onEdit = useCallback(() => {
    if (status?.inFlight) return;
    setDraft(summary?.content ?? "");
    setSaveErr(null);
    setEditing(true);
  }, [status?.inFlight, summary?.content]);

  const onSave = useCallback(async () => {
    if (!draft.trim()) {
      setSaveErr("content is required");
      return;
    }
    setSaving(true);
    setSaveErr(null);
    try {
      await api.updateRepoSummary(owner, name, {
        content: draft,
        kind: summary?.kind ?? "freeform",
      });
      setEditing(false);
      fetchSummary();
      fetchEdits();
    } catch (e) {
      setSaveErr(e instanceof ApiError ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }, [draft, owner, name, summary?.kind, fetchSummary, fetchEdits]);

  const onCancelEdit = useCallback(() => {
    setEditing(false);
    setDraft("");
    setSaveErr(null);
  }, []);

  return (
    <div className="mx-auto max-w-4xl px-6 py-6">
      <header className="mb-5 space-y-2">
        <button
          type="button"
          onClick={() => router.push("/repos")}
          className="inline-flex items-center gap-1.5 text-base font-medium text-zinc-400 transition hover:text-zinc-200"
        >
          <ArrowLeftIcon className="h-4 w-4" />
          repos
        </button>
        <div className="flex items-center gap-3">
          <GitHubIcon className="h-5 w-5 text-zinc-500" />
          <h1 className="font-mono text-lg text-zinc-100">
            {owner}/{name}
          </h1>
        </div>
      </header>

      <StatusCard status={status} loadErr={loadErr} />

      <div className="my-4 flex flex-wrap items-center gap-2">
        <button
          type="button"
          onClick={onRefresh}
          disabled={status?.inFlight === true}
          data-testid="triagent-repo-regenerate"
          className="rounded bg-zinc-100 px-3 py-1.5 text-xs font-medium text-zinc-900 transition hover:bg-white disabled:cursor-not-allowed disabled:bg-zinc-700 disabled:text-zinc-400"
        >
          {status?.inFlight ? "generating…" : "refresh"}
        </button>
        <button
          type="button"
          onClick={onEdit}
          disabled={status?.inFlight === true || editing}
          className="rounded border border-zinc-700 px-3 py-1.5 text-xs text-zinc-200 transition hover:border-zinc-500 disabled:cursor-not-allowed disabled:opacity-50"
        >
          edit
        </button>
        {edits?.hasEdits && (
          <button
            type="button"
            onClick={() => setShowEditsModal(true)}
            title="this summary has been hand-edited since the last AI generation"
            className="rounded border border-amber-700/60 bg-amber-950/40 px-2 py-1 text-[10px] font-medium text-amber-200 transition hover:border-amber-500 hover:text-amber-100"
          >
            operator edits · view diff
          </button>
        )}
        {saveErr && (
          <span className="text-xs text-red-400">{saveErr}</span>
        )}
      </div>

      {summary === null && !loadErr && (
        <div className="flex items-center gap-2 text-xs text-zinc-500">
          <Spinner className="h-3 w-3" /> loading summary…
        </div>
      )}

      {summary && !summary.exists && !editing && (
        <div
          data-testid="triagent-repo-empty-state"
          className="rounded border border-zinc-800 bg-zinc-900/40 p-4 text-sm text-zinc-400"
        >
          <p>No cached architecture summary yet.</p>
          {summary.hint && (
            <p className="mt-2 text-xs text-zinc-500">{summary.hint}</p>
          )}
          <p className="mt-3 text-xs text-zinc-500">
            Click <strong className="text-zinc-300">refresh</strong> to
            generate one. The first run can take up to 15 minutes for
            large repos.
          </p>
        </div>
      )}

      {summary && summary.exists && !editing && (
        <article
          data-testid="triagent-repo-summary"
          className={
            "prose prose-invert max-w-none " +
            "prose-headings:font-semibold prose-headings:tracking-tight " +
            "prose-h1:text-2xl prose-h1:mt-0 prose-h1:mb-4 " +
            "prose-h2:text-xl prose-h2:mt-6 prose-h2:mb-3 prose-h2:border-b prose-h2:border-zinc-800 prose-h2:pb-1 " +
            "prose-h3:text-base prose-h3:mt-4 prose-h3:mb-2 " +
            "prose-p:my-3 prose-p:leading-relaxed prose-p:text-zinc-300 " +
            "prose-li:text-zinc-300 prose-li:my-1 " +
            "prose-strong:text-zinc-100 " +
            "prose-code:before:content-[''] prose-code:after:content-[''] prose-code:bg-zinc-800 prose-code:rounded prose-code:px-1 prose-code:py-0.5 prose-code:text-[0.85em] " +
            "prose-pre:bg-zinc-950 prose-pre:border prose-pre:border-zinc-800 prose-pre:rounded prose-pre:p-3 " +
            "prose-a:text-sky-300 prose-a:no-underline hover:prose-a:underline"
          }
        >
          <ReactMarkdown remarkPlugins={[remarkGfm]}>
            {summary.content ?? ""}
          </ReactMarkdown>
        </article>
      )}

      {editing && (
        <div className="space-y-2">
          <textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            className="block h-[60vh] w-full rounded border border-zinc-800 bg-zinc-950 p-3 font-mono text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
          />
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={onSave}
              disabled={saving}
              className="rounded bg-zinc-100 px-3 py-1.5 text-xs font-medium text-zinc-900 transition hover:bg-white disabled:cursor-not-allowed disabled:bg-zinc-700 disabled:text-zinc-400"
            >
              {saving ? "saving…" : "save"}
            </button>
            <button
              type="button"
              onClick={onCancelEdit}
              disabled={saving}
              className="rounded border border-zinc-700 px-3 py-1.5 text-xs text-zinc-200 transition hover:border-zinc-500 disabled:opacity-50"
            >
              cancel
            </button>
          </div>
        </div>
      )}

      {showEditsModal && edits?.diff && (
        <EditsDiffModal
          owner={owner}
          name={name}
          diff={edits.diff}
          onClose={() => setShowEditsModal(false)}
        />
      )}

      {refreshConfirm.open && (
        <RefreshWithEditsConfirm
          owner={owner}
          name={name}
          diff={refreshConfirm.diff}
          onCancel={() => setRefreshConfirm({ open: false })}
          onPassAsContext={() => {
            setRefreshConfirm({ open: false });
            void triggerRefresh(true);
          }}
          onRegenerateFresh={() => {
            setRefreshConfirm({ open: false });
            void triggerRefresh(false);
          }}
        />
      )}
    </div>
  );
}
