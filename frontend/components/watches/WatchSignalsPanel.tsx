"use client";

import { useMemo, useState } from "react";
import type { ItemRecord, SignalRecord } from "@/lib/api";
import { SignalCard } from "@/components/watches/SignalCard";

export function WatchSignalsPanel({
  watchID,
  signals,
  items,
}: {
  watchID: string;
  signals: SignalRecord[];
  // ItemRecord[] for the same watch; SignalCard uses it to render cited
  // items inline so the operator can decide whether the signal is worth
  // starting an investigation on. Optional — empty array works fine.
  items?: ItemRecord[];
}) {
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState(false);

  // Pre-index items by ID once per items prop so per-signal lookups in
  // SignalCard are O(1).
  const itemsByID = useMemo(() => {
    const m = new Map<string, ItemRecord>();
    for (const it of items ?? []) m.set(it.id, it);
    return m;
  }, [items]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return signals;
    return signals.filter((s) => {
      if (s.id.toLowerCase().includes(q)) return true;
      if (s.outcome.toLowerCase().includes(q)) return true;
      if (s.briefing && s.briefing.toLowerCase().includes(q)) return true;
      if (s.reason && s.reason.toLowerCase().includes(q)) return true;
      // Match against any cited item's title/body so a search for an
      // issue number or error string still surfaces the signal.
      for (const id of s.citedItemIDs ?? []) {
        const it = itemsByID.get(id);
        if (!it) continue;
        const snap = it.snapshot ?? {};
        if (snap.title && snap.title.toLowerCase().includes(q)) return true;
        if (snap.body && snap.body.toLowerCase().includes(q)) return true;
        if (snap.text && snap.text.toLowerCase().includes(q)) return true;
        if (snap.author && snap.author.toLowerCase().includes(q)) return true;
      }
      return false;
    });
  }, [query, signals, itemsByID]);

  return (
    <section className="rounded border border-zinc-800">
      <header className="flex items-center gap-3 border-b border-zinc-800 px-4 py-2">
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          className="text-sm font-medium text-zinc-200"
          aria-expanded={open}
        >
          {open ? "▾" : "▸"} Signals ({filtered.length}{filtered.length !== signals.length ? ` / ${signals.length}` : ""})
        </button>
        {open && (
          <input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Filter by outcome, briefing, item content…"
            className="ml-auto w-72 rounded border border-zinc-800 bg-zinc-950 px-2 py-1 text-xs text-zinc-100 placeholder-zinc-600 focus:border-zinc-600 focus:outline-none"
            aria-label="filter signals"
          />
        )}
      </header>
      {open && (
        <div className="max-h-[60vh] space-y-2 overflow-y-auto p-3">
          {signals.length === 0 ? (
            <div className="text-sm text-zinc-500">No signals yet.</div>
          ) : filtered.length === 0 ? (
            <div className="text-sm text-zinc-500">No signals match &ldquo;{query}&rdquo;.</div>
          ) : (
            filtered.map((s) => (
              <SignalCard key={s.id} signal={s} itemsByID={itemsByID} />
            ))
          )}
        </div>
      )}
    </section>
  );
}
