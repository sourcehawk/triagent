"use client";

import { useEffect, useMemo, useState } from "react";
import { api, ApiError } from "@/lib/api";
import type { CodefixProposalListing } from "@/lib/events";
import { ExternalLinkIcon, GitHubIcon } from "./Icons";
import { Spinner } from "./Spinner";

// RepoActivityPanel renders the list of GitHub issues + PRs the
// agent opened across investigations. Lives in the repos sidenav.
// When `repo` is passed (operator focused a specific owner/name via
// ?repo=...), the panel scopes itself to that repo; otherwise it
// shows the cross-repo view.
//
// Issue-only rows (create_github_issue with no follow-up draft_pr)
// and issue+PR rows (draft_pr) are both rendered; the difference is
// whether the PR link/state shows up.
//
// Every link opens in a new browser tab — the panel is meant to be a
// quick-jump surface and shouldn't bounce the operator out of their
// current repo view.
type Props = {
  repo: string | null;
  // Bumped from the outside when the parent suspects the list might
  // be stale (e.g. an investigation that owned a row was just
  // imported). Optional.
  refreshNonce?: number;
};

export function RepoActivityPanel({ repo, refreshNonce }: Props) {
  const [items, setItems] = useState<CodefixProposalListing[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState("");

  useEffect(() => {
    let cancelled = false;
    setItems(null);
    setError(null);
    api
      .listCodefixProposals(repo ?? undefined)
      .then((list) => {
        if (cancelled) return;
        setItems(list);
      })
      .catch((e) => {
        if (cancelled) return;
        setError(e instanceof ApiError ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [repo, refreshNonce]);

  const filtered = useMemo(() => {
    if (!items) return null;
    const q = filter.trim().toLowerCase();
    if (!q) return items;
    return items.filter((p) => matchesFilter(p, q));
  }, [items, filter]);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="px-1 pb-2">
        <div className="mb-1 flex items-baseline justify-between gap-2">
          <span className="text-xs uppercase tracking-wide text-zinc-500">
            {repo ? "repository activity · this repo" : "repository activity"}
          </span>
          {items && items.length > 0 && (
            <span className="text-[10px] text-zinc-600">{items.length}</span>
          )}
        </div>
        <input
          type="search"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder={
            repo ? "search this repo…" : "search repo, issue, pr, …"
          }
          className="w-full rounded border border-zinc-800 bg-zinc-950 px-2 py-1 text-xs text-zinc-200 placeholder:text-zinc-600 focus:border-sky-600 focus:outline-none"
        />
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-1">
        {error && (
          <div className="rounded border border-red-900/60 bg-red-950/40 px-2 py-1.5 text-xs text-red-200/80">
            {error}
          </div>
        )}
        {!error && items === null && (
          <div className="flex items-center gap-2 px-1 text-xs text-zinc-500">
            <Spinner className="h-3 w-3" /> loading…
          </div>
        )}
        {!error && items && items.length === 0 && (
          <p className="px-1 text-xs text-zinc-600">
            {repo
              ? "no issues or PRs opened against this repo yet."
              : "no repository activity yet. Issues and PRs the agent opens during investigations will surface here."}
          </p>
        )}
        {!error && filtered && filtered.length === 0 && items && items.length > 0 && (
          <p className="px-1 text-xs text-zinc-600">
            nothing matches &ldquo;{filter}&rdquo;.
          </p>
        )}
        {filtered && filtered.length > 0 && (
          <ul className="space-y-1.5">
            {filtered.map((p) => (
              <ActivityRow key={p.proposal_id} p={p} showRepo={!repo} />
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

function matchesFilter(p: CodefixProposalListing, q: string): boolean {
  if (p.repo.toLowerCase().includes(q)) return true;
  if (p.summary && p.summary.toLowerCase().includes(q)) return true;
  if (p.branch_name && p.branch_name.toLowerCase().includes(q)) return true;
  if (p.issue_number && String(p.issue_number).includes(q)) return true;
  if (p.pr_number && String(p.pr_number).includes(q)) return true;
  if (p.issue_url && p.issue_url.toLowerCase().includes(q)) return true;
  if (p.pr_url && p.pr_url.toLowerCase().includes(q)) return true;
  if (p.investigation_id && p.investigation_id.toLowerCase().includes(q)) return true;
  return false;
}

function ActivityRow({
  p,
  showRepo,
}: {
  p: CodefixProposalListing;
  showRepo: boolean;
}) {
  const summaryLine = firstLine(p.summary);
  const hasPR = Boolean(p.pr_url) && p.pr_number > 0;
  const hasIssue = Boolean(p.issue_url) && p.issue_number > 0;
  return (
    <li className="rounded border border-zinc-800 bg-zinc-900/30 px-2 py-1.5 transition hover:border-zinc-700">
      {showRepo && (
        <div className="mb-0.5 flex items-center gap-1.5 text-[11px] text-zinc-400">
          <GitHubIcon className="h-3 w-3 shrink-0 text-zinc-500" />
          <span className="truncate font-mono">{p.repo}</span>
        </div>
      )}
      <div className="flex items-center gap-1.5 text-xs">
        {hasPR ? (
          <>
            <PRStateGlyph state={p.pr_state} />
            <a
              href={p.pr_url}
              target="_blank"
              rel="noreferrer noopener"
              title={p.pr_url}
              className="flex min-w-0 flex-1 items-center gap-1 truncate font-mono text-zinc-100 hover:text-sky-300"
            >
              <span className="truncate">PR #{p.pr_number}</span>
              <ExternalLinkIcon className="h-3 w-3 shrink-0 opacity-60" />
            </a>
            {hasIssue && (
              <a
                href={p.issue_url}
                target="_blank"
                rel="noreferrer noopener"
                title={p.issue_url}
                className="flex items-center gap-1 rounded border border-zinc-800 px-1 py-0.5 font-mono text-[10px] text-zinc-400 transition hover:border-zinc-600 hover:text-zinc-100"
              >
                #{p.issue_number}
                <ExternalLinkIcon className="h-2.5 w-2.5 opacity-60" />
              </a>
            )}
          </>
        ) : hasIssue ? (
          <>
            <span
              title="issue · no PR opened yet"
              aria-label="issue, no PR yet"
              className="inline-block h-2 w-2 shrink-0 rounded-full bg-amber-500"
            />
            <a
              href={p.issue_url}
              target="_blank"
              rel="noreferrer noopener"
              title={p.issue_url}
              className="flex min-w-0 flex-1 items-center gap-1 truncate font-mono text-zinc-100 hover:text-sky-300"
            >
              <span className="truncate">issue #{p.issue_number}</span>
              <ExternalLinkIcon className="h-3 w-3 shrink-0 opacity-60" />
            </a>
            <span
              title="no PR opened yet"
              className="rounded border border-zinc-800 px-1 py-0.5 text-[10px] text-zinc-500"
            >
              no PR
            </span>
          </>
        ) : (
          // Defensive: should never render — every persisted row
          // has at least one of (issue_url, pr_url). Show the
          // proposal id so an operator can grep for it.
          <span className="font-mono text-[10px] text-zinc-600">{p.proposal_id}</span>
        )}
      </div>
      {summaryLine && (
        <p className="mt-1 line-clamp-2 text-[11px] text-zinc-500">
          {summaryLine}
        </p>
      )}
      {p.investigation_id && (
        <div className="mt-1 flex items-center gap-1 text-[10px] text-zinc-600">
          <span>from</span>
          <a
            href={`/investigations/?id=${encodeURIComponent(p.investigation_id)}`}
            target="_blank"
            rel="noreferrer noopener"
            title={`open investigation ${p.investigation_id}`}
            className="flex items-center gap-0.5 font-mono text-zinc-400 transition hover:text-sky-300"
          >
            <span className="truncate">{shortInvID(p.investigation_id)}</span>
            <ExternalLinkIcon className="h-2.5 w-2.5 opacity-60" />
          </a>
          {p.created_at && (
            <>
              <span className="text-zinc-700">·</span>
              <time dateTime={p.created_at}>{formatRelative(p.created_at)}</time>
            </>
          )}
        </div>
      )}
    </li>
  );
}

function PRStateGlyph({ state }: { state: string | undefined }) {
  // Empty state ("") means the poller hasn't seen this URL yet — render
  // a neutral placeholder so the row still aligns.
  if (!state) {
    return <span className="inline-block h-2 w-2 rounded-full bg-zinc-700" aria-label="state unknown" />;
  }
  let cls = "bg-zinc-700";
  let title: string = state;
  if (state === "open") {
    cls = "bg-emerald-500";
    title = "open";
  } else if (state === "draft") {
    cls = "bg-zinc-400";
    title = "draft";
  } else if (state === "merged") {
    cls = "bg-violet-500";
    title = "merged";
  } else if (state === "closed") {
    cls = "bg-red-500";
    title = "closed";
  }
  return (
    <span
      title={title}
      aria-label={title}
      className={`inline-block h-2 w-2 shrink-0 rounded-full ${cls}`}
    />
  );
}

function firstLine(s: string): string {
  if (!s) return "";
  const trimmed = s.trim();
  const i = trimmed.indexOf("\n");
  return (i >= 0 ? trimmed.slice(0, i) : trimmed).trim();
}

function shortInvID(id: string): string {
  // Investigation IDs are long opaque strings (cuid/ulid-like). Keep
  // the head + a hint of the tail so the row stays compact but the
  // operator can still tell two adjacent rows apart.
  if (id.length <= 16) return id;
  return id.slice(0, 8) + "…" + id.slice(-4);
}

function formatRelative(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const diffSec = (Date.now() - d.getTime()) / 1000;
  if (diffSec < 60) return "just now";
  if (diffSec < 3600) return `${Math.floor(diffSec / 60)}m ago`;
  if (diffSec < 86400) return `${Math.floor(diffSec / 3600)}h ago`;
  if (diffSec < 86400 * 7) return `${Math.floor(diffSec / 86400)}d ago`;
  return d.toLocaleDateString();
}
