"use client";

import Link from "next/link";
import { useCallback, useEffect, useMemo, useState } from "react";
import { watchesAPI, type SignalRecord, type Watch } from "@/lib/api";
import { useStream } from "@/lib/stream";

// AllWatchesSignalsPanel renders the merged signal history across every
// watch on the overview page. Per-watch fetches run in parallel; results
// are sorted newest-first. The list refreshes on SSE so freshly-ingested
// signals appear without a page reload. Searchable across watch name,
// outcome, briefing, and reason.
type RowSignal = SignalRecord & {
  // Resolved watch name so each row reads "from <name>" without a
  // separate map lookup at render time.
  watchName: string;
};

export function AllWatchesSignalsPanel({ watches }: { watches: Watch[] | null }) {
  const [signals, setSignals] = useState<RowSignal[] | null>(null);
  const [query, setQuery] = useState("");
  const stream = useStream();

  // Map of watch ID → name for resolving the "from" label and for SSE
  // filtering (we only re-fetch on events scoped to a known watch).
  const watchesByID = useMemo(() => {
    const m = new Map<string, Watch>();
    for (const w of watches ?? []) m.set(w.id, w);
    return m;
  }, [watches]);

  const refresh = useCallback(async () => {
    if (!watches) return;
    if (watches.length === 0) {
      setSignals([]);
      return;
    }
    try {
      const lists = await Promise.all(
        watches.map(w =>
          watchesAPI
            .signals(w.id, { limit: 100 })
            .then(r =>
              (r.signals ?? []).map((s): RowSignal => ({ ...s, watchName: w.name })),
            )
            .catch(() => [] as RowSignal[]),
        ),
      );
      const merged = lists.flat();
      merged.sort((a, b) => (a.createdAt < b.createdAt ? 1 : -1));
      setSignals(merged);
    } catch {
      setSignals([]);
    }
  }, [watches]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  useEffect(() => {
    return stream.subscribe({ scope: "global" }, (env) => {
      if (env.kind === "signal_created" && env.signalCreated?.watchID && watchesByID.has(env.signalCreated.watchID)) {
        refresh();
      }
    });
  }, [stream, refresh, watchesByID]);

  const filtered = useMemo(() => {
    if (!signals) return null;
    const q = query.trim().toLowerCase();
    if (!q) return signals;
    return signals.filter(s => {
      if (s.watchName.toLowerCase().includes(q)) return true;
      if (s.outcome.toLowerCase().includes(q)) return true;
      if (s.id.toLowerCase().includes(q)) return true;
      if (s.briefing && s.briefing.toLowerCase().includes(q)) return true;
      if (s.reason && s.reason.toLowerCase().includes(q)) return true;
      return false;
    });
  }, [signals, query]);

  return (
    <section className="rounded border border-zinc-800">
      <header className="flex items-center gap-3 border-b border-zinc-800 px-4 py-2">
        <span className="text-sm font-medium text-zinc-200">
          Recent signals{signals ? ` (${filtered?.length ?? 0}${filtered && signals && filtered.length !== signals.length ? ` / ${signals.length}` : ""})` : ""}
        </span>
        <input
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Filter by watch, outcome, briefing…"
          className="ml-auto w-72 rounded border border-zinc-800 bg-zinc-950 px-2 py-1 text-xs text-zinc-100 placeholder-zinc-600 focus:border-zinc-600 focus:outline-none"
          aria-label="filter recent signals"
        />
      </header>
      <div className="max-h-[60vh] overflow-y-auto">
        {signals === null ? (
          <div className="px-4 py-6 text-sm text-zinc-500">Loading…</div>
        ) : signals.length === 0 ? (
          <div className="px-4 py-6 text-sm text-zinc-500">No signals across any watch yet.</div>
        ) : filtered && filtered.length === 0 ? (
          <div className="px-4 py-6 text-sm text-zinc-500">No signals match &ldquo;{query}&rdquo;.</div>
        ) : (
          <ul className="divide-y divide-zinc-800">
            {(filtered ?? []).map(s => (
              <li key={`${s.watchID}-${s.id}`} className="px-4 py-3 hover:bg-zinc-900/40">
                <Link
                  href={`/watches/${encodeURIComponent(s.watchID)}#signal-${encodeURIComponent(s.id)}`}
                  className="block"
                >
                  <div className="flex items-baseline gap-2">
                    <OutcomePill outcome={s.outcome} />
                    <span className="text-sm font-medium text-zinc-100">{s.watchName}</span>
                    <span className="text-xs text-zinc-500">
                      · {new Date(s.createdAt).toLocaleString()}
                      {s.manuallyStarted && " · (manual)"}
                    </span>
                  </div>
                  {s.briefing && (
                    <div className="mt-1 line-clamp-2 text-sm text-zinc-300">{s.briefing}</div>
                  )}
                  {s.reason && !s.briefing && (
                    <div className="mt-1 line-clamp-2 text-sm text-zinc-500">reason: {s.reason}</div>
                  )}
                  <div className="mt-1 text-xs text-zinc-500">
                    ↳ {(s.citedItemIDs ?? []).length} cited item{(s.citedItemIDs ?? []).length === 1 ? "" : "s"}
                  </div>
                </Link>
              </li>
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}

const outcomePill: Record<SignalRecord["outcome"], string> = {
  disabled: "bg-sky-900/50 text-sky-300",
  queued: "bg-amber-900/40 text-amber-300",
  investigation_started: "bg-emerald-900/50 text-emerald-300",
  proposed: "bg-amber-900/50 text-amber-300",
  unclear: "bg-amber-900/50 text-amber-300",
  dismissed: "bg-zinc-800 text-zinc-400",
  failed: "bg-red-900/50 text-red-300",
};

function OutcomePill({ outcome }: { outcome: SignalRecord["outcome"] }) {
  return (
    <span className={`inline-flex shrink-0 items-center rounded-full px-2 py-0.5 text-xs ${outcomePill[outcome]}`}>
      {outcome}
    </span>
  );
}
