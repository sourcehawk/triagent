"use client";

import { useCallback, useEffect, useState } from "react";
import { useStream } from "@/lib/stream";

type QueueSnapshot = {
  maxConcurrent: number;
  // Server emits [] when the queue is empty, but older binaries may emit
  // null — guard so a stale launcher doesn't crash the page.
  running: string[] | null;
  queued: { signalId: string; enqueuedAt: string }[] | null;
};

export function WatchQueueStrip({ watchID }: { watchID: string }) {
  const [q, setQ] = useState<QueueSnapshot | null>(null);
  const stream = useStream();

  const refresh = useCallback(() => {
    fetch(`/api/watches/${watchID}/queue`)
      .then(r => (r.ok ? r.json() : Promise.reject(r.status)))
      .then(setQ)
      .catch(() => setQ(null));
  }, [watchID]);

  useEffect(() => {
    refresh();
    const unsub = stream.subscribe({ scope: "global" }, (env) => {
      if (env.kind === "watch_status" && env.watchStatus?.watchID === watchID) {
        refresh();
      }
    });
    return unsub;
  }, [stream, watchID, refresh]);

  const running = q?.running ?? [];
  const queued = q?.queued ?? [];
  if (!q || (running.length === 0 && queued.length === 0)) return null;

  return (
    <section className="rounded border border-zinc-800 bg-zinc-900/40 p-3">
      <div className="text-sm font-medium text-zinc-200">
        Investigations — {running.length}/{q.maxConcurrent} running, {queued.length} queued
      </div>
      {queued.map((e, i) => (
        <div key={e.signalId} className="mt-1.5 flex items-center gap-2">
          <span className="text-xs text-zinc-500">#{i + 1}</span>
          <span className="font-mono text-xs text-zinc-300">{e.signalId}</span>
          <button
            type="button"
            onClick={async () => {
              await fetch(`/api/watches/${watchID}/queue/${e.signalId}/cancel`, { method: "POST" });
              refresh();
            }}
            className="ml-auto inline-flex items-center gap-1.5 rounded border border-red-900/60 bg-red-950/30 px-3 py-1.5 text-xs text-red-300 transition hover:border-red-800 hover:bg-red-950/50 hover:text-red-200"
          >
            Cancel
          </button>
        </div>
      ))}
    </section>
  );
}
