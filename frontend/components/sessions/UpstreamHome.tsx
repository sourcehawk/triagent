"use client";

import { useEffect, useMemo, useState } from "react";
import {
  api,
  ApiError,
  type SessionCard as SessionCardData,
} from "@/lib/api";
import { Paginator } from "@/components/shared/Paginator";
import { Spinner } from "@/components/shared/Spinner";
import { SessionCard } from "./SessionCard";
import { SessionUpstreamHeader } from "./SessionUpstreamHeader";

// Page size for the upstream session list. Matches the wiki home rails
// so the operator gets a consistent browse cadence across surfaces.
const PAGE_SIZE = 6;

export function UpstreamHome() {
  const [items, setItems] = useState<SessionCardData[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState("");
  const [offset, setOffset] = useState(0);

  useEffect(() => {
    // Refresh PR states first so the cards we read back reflect
    // today's open/merged/closed lifecycle, then list. The list
    // endpoint joins each upstream session card with the matching
    // local Investigation's SyncState — no client-side prBySlug map
    // needed. Both failures are non-fatal: a stale state still
    // renders sensibly and a list error surfaces in the error pane.
    void api
      .refreshPRStates()
      .catch(() => undefined)
      .then(() => api.listUpstreamSessions())
      .then(setItems)
      .catch((e) => setError(e instanceof ApiError ? e.message : String(e)));
  }, []);

  const filtered = useMemo(() => {
    if (!items) return null;
    const f = filter.trim().toLowerCase();
    if (!f) return items;
    return items.filter(
      (i) =>
        i.title.toLowerCase().includes(f) ||
        i.namespace.toLowerCase().includes(f),
    );
  }, [items, filter]);

  // Reset to first page whenever the filter changes — a typed query
  // that narrows the list to 3 hits shouldn't leave the operator on
  // page 4 of nothing.
  useEffect(() => {
    setOffset(0);
  }, [filter]);

  const total = filtered?.length ?? 0;
  const page = filtered?.slice(offset, offset + PAGE_SIZE) ?? [];

  return (
    <main className="flex-1 overflow-y-auto">
      <div className="mx-auto max-w-4xl space-y-4 px-6 py-6">
        {/* Page header: title on the left, git-sync bar on the right.
            Mirrors the wiki + playbooks layout so all three repo-backed
            views look consistent. ml-auto on the right-hand block makes
            sure the sync bar stays right-aligned even when the row
            wraps to a second line on narrow viewports — without it the
            bar drops under the title block left-aligned, which reads as
            broken. */}
        <div className="flex flex-wrap items-end gap-4">
          <header className="space-y-1">
            <h1 className="text-2xl font-semibold tracking-tight text-zinc-100">
              Investigations
            </h1>
            <p className="text-sm text-zinc-400">
              Browse pushed investigation sessions from the team's upstream
              repo, or start a new one.
            </p>
          </header>
          <div className="ml-auto">
            <SessionUpstreamHeader />
          </div>
        </div>
        <input
          type="text"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="filter by title or namespace…"
          className="w-full rounded border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-100 placeholder:text-zinc-600"
        />
        {error && (
          <div className="rounded border border-red-900/60 bg-red-950/40 p-3 text-sm text-red-200/90">
            {error}
          </div>
        )}
        {!items && !error && (
          <div className="flex items-center gap-2 text-sm text-zinc-500">
            <Spinner /> loading…
          </div>
        )}
        {items && items.length === 0 && (
          <div className="rounded border border-dashed border-zinc-800 p-6 text-center text-sm text-zinc-500">
            No upstream sessions yet. Push one with the button inside an
            archived investigation.
          </div>
        )}
        {filtered && filtered.length > 0 && (
          <div className="space-y-3">
            <div className="flex items-baseline gap-2 text-xs text-zinc-500">
              <span>
                {offset + 1}–{Math.min(offset + PAGE_SIZE, total)} of {total}
              </span>
            </div>
            <div className="space-y-2">
              {page.map((c) => (
                <SessionCard key={c.slug} card={c} />
              ))}
            </div>
            {total > PAGE_SIZE && (
              <Paginator
                offset={offset}
                pageSize={PAGE_SIZE}
                total={total}
                onPageChange={setOffset}
              />
            )}
          </div>
        )}
      </div>
    </main>
  );
}
