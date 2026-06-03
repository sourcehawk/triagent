"use client";

import { useCallback, useEffect, useState } from "react";
import { watchesAPI, type WatchIngestRunDetail, type WatchIngestRunListEntry } from "@/lib/api";
import { useStream } from "@/lib/stream";
import { Spinner } from "@/components/shared/Spinner";

// WatchIngestRunsPanel surfaces the per-poll ingestion-agent run log —
// the answer to "I see items but no signals after a poll, why?". Each
// row shows exit code, duration, item count, and a one-line preview of
// the agent's system context. Expand for the full claude stdout/stderr
// + the user prompt the agent received.
export function WatchIngestRunsPanel({
  watchID,
  ingestEnabled,
}: {
  watchID: string;
  ingestEnabled: boolean;
}) {
  const [runs, setRuns] = useState<WatchIngestRunListEntry[] | null>(null);
  const [open, setOpen] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const stream = useStream();

  const refresh = useCallback(() => {
    if (!watchID || watchID === "_") return;
    setRefreshing(true);
    watchesAPI
      .ingestRuns(watchID)
      .then((r) => setRuns(r.runs))
      .catch(() => setRuns([]))
      .finally(() => setRefreshing(false));
  }, [watchID]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  useEffect(() => {
    // Subscribe to multiple SSE event kinds so the panel refreshes
    // whenever an agent run could plausibly have produced output —
    // watch_status fires at end of every poll, signal_created fires
    // when the agent successfully writes a tool result, item_captured
    // covers the case where items land but the agent run hasn't
    // returned yet (so the placeholder appears live).
    return stream.subscribe({ scope: "global" }, (env) => {
      const wid =
        (env.kind === "watch_status" && env.watchStatus?.watchID) ||
        (env.kind === "signal_created" && env.signalCreated?.watchID) ||
        (env.kind === "item_captured" && env.itemCaptured?.watchID) ||
        (env.kind === "ingest_run_started" && env.ingestRunStarted?.watchID) ||
        (env.kind === "ingest_run_finished" && env.ingestRunFinished?.watchID);
      if (wid === watchID) refresh();
    });
  }, [stream, watchID, refresh]);

  const count = runs?.length ?? 0;
  return (
    <section className="rounded border border-zinc-800">
      <header className="flex items-center justify-between border-b border-zinc-800 px-4 py-2">
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          className="text-sm font-medium text-zinc-200"
        >
          {open ? "▾" : "▸"} Ingestion runs ({count})
        </button>
        <div className="flex items-center gap-3">
          <span className="text-xs text-zinc-500">
            The agent's per-poll output. Helpful when items arrive but no signals do.
          </span>
          <button
            type="button"
            disabled={refreshing}
            onClick={(e) => {
              e.stopPropagation();
              refresh();
            }}
            className="inline-flex items-center gap-1.5 rounded border border-zinc-700 px-2 py-0.5 text-xs text-zinc-400 transition hover:border-zinc-500 hover:text-zinc-200 disabled:cursor-not-allowed disabled:opacity-60"
            title="Re-fetch the run log list"
          >
            {refreshing && <Spinner className="h-3 w-3" />}
            {refreshing ? "refreshing…" : "refresh"}
          </button>
        </div>
      </header>
      {open && (
        <div className="max-h-[60vh] overflow-y-auto">
          {runs === null ? (
            <div className="px-4 py-6 text-sm text-zinc-500">
              <Spinner className="mr-2 inline-block" /> Loading runs…
            </div>
          ) : runs.length === 0 ? (
            <div className="px-4 py-6 text-sm text-zinc-500">
              {ingestEnabled
                ? "No ingestion runs yet. They'll appear here after the next poll that captures at least one non-filtered item."
                : "Auto-ingest is off for this watch — the agent never runs, so there are no logs. Enable Auto-ingest in the watch's edit view to start producing classified signals (and these logs)."}
            </div>
          ) : (
            <ul className="divide-y divide-zinc-800">
              {runs.map((r) => (
                <IngestRunRow key={r.id} watchID={watchID} run={r} />
              ))}
            </ul>
          )}
        </div>
      )}
    </section>
  );
}

function IngestRunRow({ watchID, run }: { watchID: string; run: WatchIngestRunListEntry }) {
  const [expanded, setExpanded] = useState(false);
  const [detail, setDetail] = useState<WatchIngestRunDetail | null>(null);
  const [loadErr, setLoadErr] = useState<string | null>(null);

  const status = run.status ?? (run.exitCode === 0 && !run.error ? "ok" : "error");
  const presentation: Record<string, { accent: string; pill: string; label: string }> = {
    running: { accent: "border-l-amber-400", pill: "bg-amber-900/50 text-amber-300", label: "running" },
    ok: { accent: "border-l-emerald-500", pill: "bg-emerald-900/50 text-emerald-300", label: "ok" },
    error: { accent: "border-l-red-500", pill: "bg-red-900/50 text-red-300", label: run.exitCode ? `exit ${run.exitCode}` : "error" },
    skipped: { accent: "border-l-zinc-500", pill: "bg-zinc-800 text-zinc-400", label: "skipped" },
  };
  const p = presentation[status] ?? presentation.error;
  const elapsed = run.durationMs > 0 ? `${run.durationMs}ms` : status === "running" ? `${Math.max(0, Math.round((Date.now() - new Date(run.startedAt).getTime()) / 1000))}s elapsed` : "—";

  async function toggle() {
    const next = !expanded;
    setExpanded(next);
    if (next && !detail && !loadErr) {
      try {
        const d = await watchesAPI.ingestRun(watchID, run.id);
        setDetail(d);
      } catch (e) {
        setLoadErr(e instanceof Error ? e.message : String(e));
      }
    }
  }

  return (
    <li className={`border-l-4 px-4 py-2 hover:bg-zinc-900/30 ${p.accent}`}>
      <button type="button" onClick={toggle} className="block w-full text-left">
        <div className="flex items-baseline gap-2">
          <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs ${p.pill}`}>
            {p.label}
          </span>
          <span className="text-sm text-zinc-300">{new Date(run.startedAt).toLocaleString()}</span>
          <span className="text-xs text-zinc-500">
            · {run.itemCount} item{run.itemCount === 1 ? "" : "s"} · {elapsed}
          </span>
          <span className="ml-auto text-xs text-zinc-500">{expanded ? "▾" : "▸"}</span>
        </div>
        {run.error && !expanded && (
          <div className="mt-1 truncate text-xs text-red-300">{run.error}</div>
        )}
      </button>
      {expanded && (
        <div className="mt-2 space-y-2 text-xs">
          {run.systemHint && (
            <div>
              <div className="mb-0.5 uppercase tracking-wide text-zinc-500">System hint</div>
              <div className="rounded border border-zinc-800 bg-zinc-950 p-2 text-zinc-400">{run.systemHint}</div>
            </div>
          )}
          {detail === null && !loadErr && (
            <div className="text-zinc-500">
              <Spinner className="mr-2 inline-block" /> Loading log…
            </div>
          )}
          {loadErr && <div className="text-red-300">Failed to load log: {loadErr}</div>}
          {detail && (
            <>
              {detail.userPrompt && (
                <details className="rounded border border-zinc-800 bg-zinc-950">
                  <summary className="cursor-pointer px-2 py-1 text-zinc-400">
                    User prompt the agent received ({detail.userPrompt.length} chars)
                  </summary>
                  <pre className="max-h-64 overflow-y-auto whitespace-pre-wrap break-words border-t border-zinc-800 p-2 font-mono text-[11px] text-zinc-300">
                    {detail.userPrompt}
                  </pre>
                </details>
              )}
              {detail.error && (
                <div className="rounded border border-red-900/60 bg-red-950/30 p-2 text-red-200/90">
                  <div className="mb-0.5 text-[10px] uppercase tracking-wide text-red-400">Spawn error</div>
                  {detail.error}
                </div>
              )}
              <div>
                <div className="mb-0.5 uppercase tracking-wide text-zinc-500">claude stdout + stderr</div>
                <pre className="max-h-[40vh] overflow-y-auto whitespace-pre-wrap break-words rounded border border-zinc-800 bg-zinc-950 p-2 font-mono text-[11px] text-zinc-300">
                  {detail.log || "(no output)"}
                </pre>
              </div>
            </>
          )}
        </div>
      )}
    </li>
  );
}
