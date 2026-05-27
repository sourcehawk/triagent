"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { wikiApi, ApiError } from "@/lib/wiki-api";
import type { WikiStats, WikiEntryHit } from "@/lib/wiki-api";
import { api, type Investigation } from "@/lib/api";
import { labelFor } from "@/lib/sidebar-label";
import { Spinner } from "@/components/Spinner";
import { WikiStats as WikiStatsStrip } from "./WikiStats";
import { WikiSearch, type WikiFilters, emptyFilters } from "./WikiSearch";
import { EntryList } from "./EntryList";
import { WikiUpstreamHeader } from "./WikiUpstreamHeader";
import { Paginator } from "@/components/Paginator";

// Page size for the entry list. Matches the other home-page rails
// (sessions, upstream) so the operator sees a consistent cadence
// across surfaces — the search bar handles "find the specific one I
// want" and the paginator handles "browse".
const PAGE_SIZE = 6;

// Page size for the "sessions without wiki" rail. Same cadence as the
// entry list above.
const UNCORR_PAGE_SIZE = 6;

export function WikiHome() {
  const [stats, setStats] = useState<WikiStats | null>(null);
  const [hits, setHits] = useState<WikiEntryHit[] | null>(null);
  const [total, setTotal] = useState(0);
  const [offset, setOffset] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [notConfigured, setNotConfigured] = useState(false);
  const [loading, setLoading] = useState(true);
  const [searching, setSearching] = useState(false);
  const [filters, setFilters] = useState<WikiFilters>(emptyFilters);
  // Local investigations whose IDs don't appear in any wiki entry's
  // links.investigation. These become candidates to start a wiki
  // creation flow from. Loaded independently of the entry list so a
  // 503 from the wiki vault doesn't hide them.
  const [uncorrelated, setUncorrelated] = useState<Investigation[]>([]);

  // Initial load: stats + first page in parallel.
  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);

    Promise.all([
      wikiApi.getStats(),
      wikiApi.listEntries({ limit: PAGE_SIZE, offset: 0 }),
    ])
      .then(([s, i]) => {
        if (cancelled) return;
        setStats(s);
        setHits(i.hits);
        setTotal(i.total);
        setOffset(0);
        setLoading(false);
      })
      .catch((err) => {
        if (cancelled) return;
        if (err instanceof ApiError && err.status === 503) {
          setNotConfigured(true);
          setLoading(false);
          return;
        }
        setError(err instanceof ApiError ? err.message : String(err));
        setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, []);

  // Independently load investigations + correlated session ids, then
  // compute the uncorrelated set. Kept off the main loading path so the
  // incident list renders even if the investigations endpoint hiccups,
  // and so a not-yet-configured wiki (503 from correlated-sessions)
  // still shows the local sessions list.
  useEffect(() => {
    let cancelled = false;
    Promise.all([
      api.listInvestigations(),
      wikiApi.listCorrelatedSessions().catch((err) => {
        // 503 = wiki not configured — treat as empty correlation set
        // so every local session shows up as a candidate.
        if (err instanceof ApiError && err.status === 503) {
          return { session_ids: [] };
        }
        throw err;
      }),
    ])
      .then(([invs, corr]) => {
        if (cancelled) return;
        const taken = new Set(corr.session_ids);
        // Skip archived sessions — they're rarely going to seed a wiki.
        const candidates = invs.filter(
          (inv) => !inv.archived && !taken.has(inv.id),
        );
        setUncorrelated(candidates);
      })
      .catch(() => {
        if (cancelled) return;
        // Non-fatal: leave the section empty rather than blocking the page.
        setUncorrelated([]);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Fetch one page for the current filters + offset. Single helper so the
  // initial-load, filter-change, and page-change paths stay consistent.
  const fetchPage = useCallback(
    (f: WikiFilters, nextOffset: number) => {
      setSearching(true);
      wikiApi
        .listEntries({
          q: f.q || undefined,
          services: f.services || undefined,
          errors: f.errors || undefined,
          symptoms: f.symptoms || undefined,
          severity: f.severity || undefined,
          status: f.status || undefined,
          unsynced: f.unsynced || undefined,
          limit: PAGE_SIZE,
          offset: nextOffset,
        })
        .then((res) => {
          setHits(res.hits);
          setTotal(res.total);
          setOffset(res.offset);
          setSearching(false);
        })
        .catch((err) => {
          if (err instanceof ApiError && err.status !== 503) {
            setError(err.message);
          }
          setSearching(false);
        });
    },
    [],
  );

  // Re-search when filters change (debounced by WikiSearch). Always
  // resets to offset 0 — a filter change invalidates the current page.
  const handleSearch = useCallback(
    (f: WikiFilters) => {
      setFilters(f);
      fetchPage(f, 0);
    },
    [fetchPage],
  );

  const handlePageChange = useCallback(
    (nextOffset: number) => {
      fetchPage(filters, nextOffset);
    },
    [fetchPage, filters],
  );

  // After an upstream sync, the local checkout has moved — entries that
  // were "unsynced" may now be in sync (or, less commonly, vice-versa),
  // and stats may shift if upstream had promotions we didn't. Refresh
  // the current page in place so the cloud-up icons fall away without
  // the operator having to reload.
  const handleSynced = useCallback(() => {
    fetchPage(filters, offset);
    wikiApi
      .getStats()
      .then(setStats)
      .catch(() => {
        // Non-fatal — stats are nice-to-have, the entry-list refresh is
        // the user-visible bit.
      });
  }, [fetchPage, filters, offset]);

  if (loading) {
    return (
      <div className="flex items-center gap-2 px-6 py-12 text-sm text-zinc-400">
        <Spinner /> loading wiki…
      </div>
    );
  }

  if (notConfigured) {
    return <WikiNotConfigured />;
  }

  if (error) {
    return (
      <div className="mx-auto max-w-3xl px-6 py-12">
        <div className="rounded-lg border border-red-900/60 bg-red-950/40 p-4 text-sm text-red-200/90">
          {error}
        </div>
      </div>
    );
  }

  const isFiltering =
    filters.q !== "" ||
    filters.services !== "" ||
    filters.errors !== "" ||
    filters.symptoms !== "" ||
    filters.severity !== "" ||
    filters.status !== "" ||
    filters.unsynced;

  return (
    <div className="mx-auto max-w-6xl px-6 py-10">
      {/* Header row: title left, upstream strip right */}
      <div className="mb-6 flex flex-wrap items-end justify-between gap-4">
        <header className="space-y-1">
          <h1 className="text-2xl font-semibold tracking-tight text-zinc-100">
            Investigation wiki
          </h1>
          <p className="text-sm text-zinc-400">
            Promoted incident post-mortems and entity knowledge base.
          </p>
        </header>
        <WikiUpstreamHeader onSynced={handleSynced} />
      </div>

      {/* Stats strip */}
      {stats && (
        <div className="mb-6">
          <WikiStatsStrip stats={stats} />
        </div>
      )}

      {/* Incident list + search (full width — entity browser is in WikiSideNav) */}
      <div className="space-y-6">
        {/* Search + filters */}
        <section>
          <WikiSearch onSearch={handleSearch} />
        </section>

        {/* Recent / filtered entries */}
        <section className="space-y-3">
          <div className="flex items-baseline gap-2">
            <h2 className="text-sm font-semibold uppercase tracking-wide text-zinc-400">
              {isFiltering ? "Matching entries" : "Recent entries"}
            </h2>
            {total > 0 && (
              <span className="text-xs text-zinc-500">
                {offset + 1}–{Math.min(offset + PAGE_SIZE, total)} of {total}
              </span>
            )}
            {searching && <Spinner className="h-3 w-3" />}
          </div>
          {hits !== null && (
            <EntryList hits={hits} isFiltering={isFiltering} />
          )}
          {total > PAGE_SIZE && (
            <Paginator
              offset={offset}
              pageSize={PAGE_SIZE}
              total={total}
              disabled={searching}
              onPageChange={handlePageChange}
            />
          )}
        </section>

        {/* Sessions without a wiki yet — each row navigates into the
            session view, where the operator can ask the agent to draft a
            wiki entry. The wiki page doesn't kick off creation itself;
            it just surfaces the candidates. */}
        {!isFiltering && (
          <UncorrelatedSessions sessions={uncorrelated} />
        )}
      </div>
    </div>
  );
}

type UncorrelatedSessionsProps = {
  sessions: Investigation[];
};

function UncorrelatedSessions({ sessions }: UncorrelatedSessionsProps) {
  // Client-side paginate — the whole list is already in memory (one
  // /api/investigations call), so slicing here keeps the UI snappy
  // without a new endpoint. Clamp offset on prop change so a deletion
  // upstream doesn't strand us past the last page.
  const [offset, setOffset] = useState(0);
  const total = sessions.length;
  const maxOffset = Math.max(0, total - 1);
  const clampedOffset = Math.min(offset, maxOffset - (maxOffset % UNCORR_PAGE_SIZE));
  const page = sessions.slice(clampedOffset, clampedOffset + UNCORR_PAGE_SIZE);
  return (
    <section className="space-y-3 border-t border-zinc-800 pt-6">
      <div className="flex items-baseline gap-2">
        <h2 className="text-sm font-semibold uppercase tracking-wide text-zinc-400">
          Recent sessions without wiki
        </h2>
        {total > 0 && (
          <span className="text-xs text-zinc-500">
            {clampedOffset + 1}–{Math.min(clampedOffset + UNCORR_PAGE_SIZE, total)} of {total}
          </span>
        )}
      </div>
      <p className="text-xs text-zinc-500">
        Local investigations that don't yet have a wiki entry. Open one to
        draft its wiki from inside the session.
      </p>
      {total === 0 ? (
        <div className="rounded-lg border border-zinc-800 bg-zinc-950/40 px-4 py-3 text-sm text-zinc-400">
          No sessions without wikis! ✓
        </div>
      ) : (
        <>
          <ul className="divide-y divide-zinc-900 rounded-lg border border-zinc-800 bg-zinc-950/40">
            {page.map((inv) => {
              const lbl = labelFor(inv);
              return (
                <li key={inv.id}>
                  <Link
                    href={`/investigations/?id=${encodeURIComponent(inv.id)}`}
                    className="flex items-baseline justify-between gap-3 px-4 py-2.5 transition hover:bg-zinc-900/40"
                  >
                    <span className="flex min-w-0 items-baseline gap-2">
                      <span
                        className={
                          "truncate text-sm " +
                          (lbl.placeholder
                            ? "italic text-zinc-500"
                            : "text-zinc-200")
                        }
                      >
                        {lbl.text}
                      </span>
                      {inv.namespace && (
                        <span className="truncate font-mono text-xs text-zinc-500">
                          {inv.namespace}
                        </span>
                      )}
                    </span>
                    <span className="shrink-0 text-xs text-zinc-600">
                      <time dateTime={inv.createdAt}>
                        {formatRelative(inv.createdAt)}
                      </time>
                    </span>
                  </Link>
                </li>
              );
            })}
          </ul>
          {total > UNCORR_PAGE_SIZE && (
            <Paginator
              offset={clampedOffset}
              pageSize={UNCORR_PAGE_SIZE}
              total={total}
              disabled={false}
              onPageChange={setOffset}
            />
          )}
        </>
      )}
    </section>
  );
}

function formatRelative(iso: string): string {
  const d = new Date(iso);
  const diffSec = (Date.now() - d.getTime()) / 1000;
  if (diffSec < 60) return "just now";
  if (diffSec < 3600) return `${Math.floor(diffSec / 60)}m ago`;
  if (diffSec < 86400) return `${Math.floor(diffSec / 3600)}h ago`;
  if (diffSec < 86400 * 7) return `${Math.floor(diffSec / 86400)}d ago`;
  return d.toLocaleDateString();
}

function WikiNotConfigured() {
  return (
    <div className="mx-auto max-w-2xl px-6 py-16">
      <div className="rounded-lg border border-zinc-800 bg-zinc-900/40 px-6 py-10 text-center space-y-3">
        <h2 className="text-lg font-semibold text-zinc-200">
          Wiki vault not configured
        </h2>
        <p className="text-sm text-zinc-400">
          Start the launcher with{" "}
          <code className="rounded bg-zinc-800 px-1.5 py-0.5 font-mono text-xs text-zinc-200">
            --wiki-path /path/to/vault
          </code>{" "}
          or{" "}
          <code className="rounded bg-zinc-800 px-1.5 py-0.5 font-mono text-xs text-zinc-200">
            --wiki-repo OWNER/REPO
          </code>{" "}
          to enable the wiki browser.
        </p>
        <p className="text-xs text-zinc-600">
          The wiki vault is a git-backed directory of incident post-mortems and
          entity stubs that the investigation agent can read from and write to.
        </p>
      </div>
    </div>
  );
}
