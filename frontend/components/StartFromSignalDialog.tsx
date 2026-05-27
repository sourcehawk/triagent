"use client";

import { useState } from "react";

export function StartFromSignalDialog({
  watchID,
  signalID,
  initialClusters,
  onClose,
}: {
  watchID: string;
  signalID: string;
  // Comma-joined cluster hints the ingestion agent attached to the
  // signal (signal.clusters). Pre-fills the cluster field so the
  // operator doesn't have to copy/paste from the briefing.
  initialClusters?: string[];
  onClose: (invID?: string) => void;
}) {
  const [clusters, setClusters] = useState(initialClusters?.join(", ") ?? "");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit() {
    setSubmitting(true);
    setError(null);
    try {
      const res = await fetch(`/api/watches/${encodeURIComponent(watchID)}/signals/${encodeURIComponent(signalID)}/start`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          clusters: clusters.split(",").map(s => s.trim()).filter(Boolean),
        }),
      });
      if (!res.ok) {
        const text = await res.text().catch(() => "");
        throw new Error(text || `${res.status} ${res.statusText}`);
      }
      const body: { investigationId?: string } = await res.json();
      onClose(body.investigationId);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setSubmitting(false);
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4"
      onClick={() => onClose()}
    >
      <div
        className="max-h-[80vh] w-full max-w-md overflow-y-auto rounded border border-zinc-800 bg-zinc-950 p-5 text-sm shadow-xl"
        onClick={e => e.stopPropagation()}
        role="dialog"
        aria-label="Start investigation from signal"
      >
        <h2 className="mb-4 text-base font-semibold text-zinc-100">Start investigation from signal</h2>
        <label className="block">
          <span className="mb-1 block text-xs uppercase tracking-wide text-zinc-400">
            Suggested cluster(s){" "}
            <span className="normal-case text-zinc-500">(optional, comma-separated)</span>
          </span>
          <input
            value={clusters}
            onChange={e => setClusters(e.target.value)}
            className={inputClass}
            placeholder="prod-emea-1, prod-us-2"
          />
        </label>
        {error && (
          <div className="mt-2 text-sm text-red-400">{error}</div>
        )}
        <div className="mt-5 flex items-center justify-end gap-2">
          <button
            type="button"
            onClick={() => onClose()}
            disabled={submitting}
            className="inline-flex items-center gap-1.5 rounded border border-zinc-700 bg-zinc-900/60 px-3 py-1.5 text-xs text-zinc-300 transition hover:border-zinc-600 hover:text-zinc-100 disabled:opacity-50"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={submit}
            disabled={submitting}
            className="inline-flex items-center gap-1.5 rounded bg-zinc-100 px-4 py-2 text-sm font-medium text-zinc-900 transition hover:bg-white disabled:cursor-not-allowed disabled:bg-zinc-700 disabled:text-zinc-400"
          >
            {submitting ? "Starting…" : "Start investigation"}
          </button>
        </div>
      </div>
    </div>
  );
}

const inputClass =
  "w-full rounded border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-100 placeholder-zinc-600 focus:border-zinc-600 focus:outline-none";
