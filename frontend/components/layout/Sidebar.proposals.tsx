"use client";

import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { api, ApiError, type PlaybookProposalListItem } from "@/lib/api";
import { wikiApi, type WikiProposalListItem } from "@/lib/wiki-api";
import { useDialog } from "@/lib/dialog";

export function RelatedSection({
  recent,
  outgoing,
  inverse,
  onPick,
}: {
  recent: string[];
  outgoing: { delegates: string[]; handoffs: string[] };
  inverse: { delegatedFrom: string[]; handoffsFrom: string[] };
  onPick: (id: string) => void;
}) {
  const [open, setOpen] = useState(true);
  const groups: Array<{ title: string; ids: string[]; subtitle?: string }> = [];
  if (recent.length > 0) groups.push({ title: "Recent", ids: recent });
  if (outgoing.delegates.length > 0)
    groups.push({ title: "Delegates", ids: outgoing.delegates, subtitle: "this playbook walks" });
  if (outgoing.handoffs.length > 0)
    groups.push({ title: "Handoffs", ids: outgoing.handoffs, subtitle: "this playbook hands to" });
  if (inverse.delegatedFrom.length > 0)
    groups.push({ title: "Delegated by", ids: inverse.delegatedFrom, subtitle: "playbooks that walk this" });
  if (inverse.handoffsFrom.length > 0)
    groups.push({ title: "Handed to from", ids: inverse.handoffsFrom, subtitle: "playbooks that hand to this" });

  return (
    <section className="border-b border-zinc-800/60 px-3 py-2">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="flex w-full items-center justify-between text-left text-xs font-medium uppercase tracking-wide text-zinc-500 transition hover:text-zinc-300"
      >
        <span>Related</span>
        <span aria-hidden className="text-zinc-600">{open ? "▾" : "▸"}</span>
      </button>
      {open && (
        <div className="mt-2 space-y-2">
          {groups.map((g) => (
            <div key={g.title}>
              <div className="text-[10px] uppercase tracking-wide text-zinc-600">
                {g.title}
                {g.subtitle && (
                  <span className="ml-1 normal-case text-zinc-700">— {g.subtitle}</span>
                )}
              </div>
              <ul className="mt-1 space-y-0.5">
                {g.ids.map((id) => (
                  <li key={`${g.title}-${id}`}>
                    <button
                      type="button"
                      onClick={() => onPick(id)}
                      className="block w-full truncate rounded px-1.5 py-0.5 text-left font-mono text-xs text-zinc-300 transition hover:bg-zinc-800/60 hover:text-zinc-100"
                      title={id}
                    >
                      {id}
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}

// WikiPendingProposals lists wiki drafts that haven't been approved or
// declined yet. Lets an operator who tabbed out of the editor jump
// straight back to the AI proposal tab on the matching entry. Listens
// for c1:wiki-approved (the approve flow's window event) and a custom
// c1:wiki-proposals-changed event so decline/refresh elsewhere
// invalidates the list in-place.
export function WikiPendingProposals({
  refreshNonce,
}: {
  refreshNonce?: number;
}) {
  const [items, setItems] = useState<WikiProposalListItem[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState("");
  const dialog = useDialog();

  // Filter against slug and (optional) title. The proposal list shape
  // doesn't carry the draft body, so this is a name/title match — not a
  // full-text content search. Case-insensitive substring is enough at
  // the scale the section is bounded to.
  const visibleItems = useMemo(() => {
    if (!items) return null;
    const q = filter.trim().toLowerCase();
    if (q === "") return items;
    return items.filter((p) => {
      if (p.slug.toLowerCase().includes(q)) return true;
      if (p.title && p.title.toLowerCase().includes(q)) return true;
      return false;
    });
  }, [items, filter]);

  useEffect(() => {
    let cancelled = false;
    const refetch = () => {
      wikiApi
        .listProposals()
        .then((res) => {
          if (!cancelled) {
            setItems(res.proposals);
            setError(null);
          }
        })
        .catch((err) => {
          if (cancelled) return;
          // 503 = wiki not configured. Treat as "no proposals" rather
          // than an error — the home view explains the config gap.
          if (err instanceof ApiError && err.status === 503) {
            setItems([]);
            return;
          }
          setError(err instanceof ApiError ? err.message : String(err));
        });
    };
    refetch();
    const onChange = () => refetch();
    window.addEventListener("c1:wiki-approved", onChange);
    window.addEventListener("c1:wiki-proposals-changed", onChange);
    return () => {
      cancelled = true;
      window.removeEventListener("c1:wiki-approved", onChange);
      window.removeEventListener("c1:wiki-proposals-changed", onChange);
    };
  }, [refreshNonce]);

  async function deleteProposal(p: WikiProposalListItem, e: React.MouseEvent) {
    e.preventDefault();
    e.stopPropagation();
    const ok = await dialog.confirm({
      title: "Discard this proposal?",
      body: `The draft for ${p.slug} will be removed from the local proposals dir. The agent's chat card will switch to "declined".`,
      confirmLabel: "Discard",
      danger: true,
    });
    if (!ok) return;
    try {
      await api.declineWikiProposal(p.proposal_id);
      setItems((prev) =>
        prev?.filter((i) => i.proposal_id !== p.proposal_id) ?? prev,
      );
      window.dispatchEvent(new CustomEvent("c1:wiki-proposals-changed"));
    } catch (err) {
      await dialog.alert({
        title: "Discard failed",
        body: err instanceof ApiError ? err.message : String(err),
      });
    }
  }

  return (
    <section className="border-b border-zinc-800/60 px-3 pb-3">
      <div className="mb-2 text-xs font-medium uppercase tracking-wide text-zinc-500">
        Active proposals
        {items && items.length > 0 && (
          <span className="ml-1 text-zinc-600">
            ({visibleItems && visibleItems.length !== items.length
              ? `${visibleItems.length}/${items.length}`
              : items.length})
          </span>
        )}
      </div>
      {error && (
        <div className="rounded border border-red-900/60 bg-red-950/40 px-2 py-1 text-[11px] text-red-200/80">
          {error}
        </div>
      )}
      {items && items.length === 0 && !error && (
        <p className="px-1 text-[11px] text-zinc-600">No pending proposals.</p>
      )}
      {items && items.length > 0 && (
        <input
          type="search"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="Filter by name or title…"
          className="mb-1.5 w-full rounded border border-zinc-800 bg-zinc-950 px-2 py-1 text-[11px] text-zinc-200 placeholder-zinc-600 focus:border-zinc-700 focus:outline-none"
        />
      )}
      {visibleItems && visibleItems.length === 0 && items && items.length > 0 && (
        <p className="px-1 text-[11px] text-zinc-600">
          No proposals match "{filter}".
        </p>
      )}
      {visibleItems && visibleItems.length > 0 && (
        <ul className="space-y-0.5">
          {visibleItems.map((p) => (
            <li key={p.proposal_id}>
              <Link
                href={`/wiki/entries/?slug=${encodeURIComponent(p.slug)}&proposal=${encodeURIComponent(p.proposal_id)}&tab=proposal`}
                className="group flex w-full flex-col items-start gap-0.5 rounded px-2 py-1.5 text-left transition hover:bg-zinc-900"
              >
                <div className="flex w-full items-baseline justify-between gap-2">
                  <span className="flex min-w-0 flex-1 items-center gap-1.5 truncate text-xs text-zinc-100 group-hover:overflow-visible group-hover:whitespace-normal group-hover:break-words">
                    <span
                      aria-hidden
                      className="inline-block h-1.5 w-1.5 shrink-0 animate-pulse rounded-full bg-sky-400"
                    />
                    <span className="min-w-0 flex-1 truncate font-mono group-hover:overflow-visible group-hover:whitespace-normal group-hover:break-all">
                      {p.slug}
                    </span>
                  </span>
                  {p.is_new ? (
                    <span className="shrink-0 rounded bg-emerald-900/40 px-1 py-0.5 text-[9px] font-mono text-emerald-300">
                      new
                    </span>
                  ) : (
                    <span className="shrink-0 rounded bg-amber-900/40 px-1 py-0.5 text-[9px] font-mono text-amber-300">
                      update
                    </span>
                  )}
                  <button
                    type="button"
                    onClick={(e) => void deleteProposal(p, e)}
                    aria-label="discard proposal"
                    className="shrink-0 text-xs text-zinc-600 opacity-0 transition group-hover:opacity-100 hover:text-red-400"
                  >
                    ✕
                  </button>
                </div>
                {p.title && (
                  <div className="w-full truncate pl-3 text-[11px] text-zinc-500 group-hover:overflow-visible group-hover:whitespace-normal group-hover:break-words group-hover:text-zinc-400">
                    {p.title}
                  </div>
                )}
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

// PlaybookPendingProposals lists playbook drafts that haven't been
// approved or declined yet. Mirrors WikiPendingProposals so an operator
// who tabbed away from a proposal can re-open it from the sidenav.
// Listens for c1:playbook-proposals-changed (fired by approve/decline
// flows) so the list invalidates in-place.
export function PlaybookPendingProposals({
  refreshNonce,
}: {
  refreshNonce?: number;
}) {
  const [items, setItems] = useState<PlaybookProposalListItem[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState("");
  const dialog = useDialog();

  const visibleItems = useMemo(() => {
    if (!items) return null;
    const q = filter.trim().toLowerCase();
    if (q === "") return items;
    return items.filter((p) => {
      if (p.playbook_id.toLowerCase().includes(q)) return true;
      if (p.description && p.description.toLowerCase().includes(q)) return true;
      if (p.type.toLowerCase().includes(q)) return true;
      return false;
    });
  }, [items, filter]);

  useEffect(() => {
    let cancelled = false;
    const refetch = () => {
      api
        .listPlaybookProposals()
        .then((res) => {
          if (!cancelled) {
            setItems(res.proposals);
            setError(null);
          }
        })
        .catch((err) => {
          if (cancelled) return;
          // 503 = playbooks dir not configured. Treat as "no proposals"
          // — the main pane already surfaces the config gap.
          if (err instanceof ApiError && err.status === 503) {
            setItems([]);
            return;
          }
          setError(err instanceof ApiError ? err.message : String(err));
        });
    };
    refetch();
    const onChange = () => refetch();
    window.addEventListener("c1:playbook-proposals-changed", onChange);
    return () => {
      cancelled = true;
      window.removeEventListener("c1:playbook-proposals-changed", onChange);
    };
  }, [refreshNonce]);

  async function deleteProposal(
    p: PlaybookProposalListItem,
    e: React.MouseEvent,
  ) {
    e.preventDefault();
    e.stopPropagation();
    const ok = await dialog.confirm({
      title: "Discard this proposal?",
      body: `The draft for ${p.playbook_id} will be removed from the local proposals dir. The agent's chat card will switch to "declined".`,
      confirmLabel: "Discard",
      danger: true,
    });
    if (!ok) return;
    try {
      await api.declinePlaybookProposal(p.proposal_id);
      setItems((prev) =>
        prev?.filter((i) => i.proposal_id !== p.proposal_id) ?? prev,
      );
      window.dispatchEvent(new CustomEvent("c1:playbook-proposals-changed"));
    } catch (err) {
      await dialog.alert({
        title: "Discard failed",
        body: err instanceof ApiError ? err.message : String(err),
      });
    }
  }

  // Render nothing while loading / when there are no proposals so the
  // sidenav stays compact for the common case. The wiki equivalent
  // does render an empty-state line, but on the playbooks page the
  // sidenav already carries a couple of explanatory paragraphs, and
  // an extra "No pending proposals." row pushes them down for no win.
  if (!items || items.length === 0) {
    return error ? (
      <section className="border-b border-zinc-800/60 px-3 pb-3">
        <div className="mb-2 text-xs font-medium uppercase tracking-wide text-zinc-500">
          Active proposals
        </div>
        <div className="rounded border border-red-900/60 bg-red-950/40 px-2 py-1 text-[11px] text-red-200/80">
          {error}
        </div>
      </section>
    ) : null;
  }

  return (
    <section className="border-b border-zinc-800/60 px-3 pb-3">
      <div className="mb-2 text-xs font-medium uppercase tracking-wide text-zinc-500">
        Active proposals
        <span className="ml-1 text-zinc-600">
          ({visibleItems && visibleItems.length !== items.length
            ? `${visibleItems.length}/${items.length}`
            : items.length})
        </span>
      </div>
      <input
        type="search"
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
        placeholder="Filter by id, type or description…"
        className="mb-1.5 w-full rounded border border-zinc-800 bg-zinc-950 px-2 py-1 text-[11px] text-zinc-200 placeholder-zinc-600 focus:border-zinc-700 focus:outline-none"
      />
      {visibleItems && visibleItems.length === 0 && (
        <p className="px-1 text-[11px] text-zinc-600">
          No proposals match "{filter}".
        </p>
      )}
      {visibleItems && visibleItems.length > 0 && (
        <ul className="space-y-0.5">
          {visibleItems.map((p) => (
            <li key={p.proposal_id}>
              <Link
                data-testid="triagent-playbook-proposal"
                data-playbook-id={p.playbook_id}
                data-proposal-id={p.proposal_id}
                href={`/playbooks?playbook=${encodeURIComponent(p.playbook_id)}&proposal=${encodeURIComponent(p.proposal_id)}&tab=proposal`}
                className="group flex w-full flex-col items-start gap-0.5 rounded px-2 py-1.5 text-left transition hover:bg-zinc-900"
              >
                <div className="flex w-full items-baseline justify-between gap-2">
                  <span className="flex min-w-0 flex-1 items-center gap-1.5 truncate text-xs text-zinc-100 group-hover:overflow-visible group-hover:whitespace-normal group-hover:break-words">
                    <span
                      aria-hidden
                      className="inline-block h-1.5 w-1.5 shrink-0 animate-pulse rounded-full bg-sky-400"
                    />
                    <span className="min-w-0 flex-1 truncate font-mono group-hover:overflow-visible group-hover:whitespace-normal group-hover:break-all">
                      {p.playbook_id}
                    </span>
                  </span>
                  {p.is_new ? (
                    <span
                      data-testid="triagent-playbook-proposal-badge"
                      className="shrink-0 rounded bg-emerald-900/40 px-1 py-0.5 text-[9px] font-mono text-emerald-300"
                    >
                      new
                    </span>
                  ) : (
                    <span
                      data-testid="triagent-playbook-proposal-badge"
                      className="shrink-0 rounded bg-amber-900/40 px-1 py-0.5 text-[9px] font-mono text-amber-300"
                    >
                      update
                    </span>
                  )}
                  <button
                    type="button"
                    onClick={(e) => void deleteProposal(p, e)}
                    aria-label="discard proposal"
                    className="shrink-0 text-xs text-zinc-600 opacity-0 transition group-hover:opacity-100 hover:text-red-400"
                  >
                    ✕
                  </button>
                </div>
                <div className="w-full truncate pl-3 text-[11px] text-zinc-500 group-hover:overflow-visible group-hover:whitespace-normal group-hover:break-words group-hover:text-zinc-400">
                  <span className="text-zinc-600">{p.type}</span>
                  {p.description && (
                    <>
                      <span className="mx-1 text-zinc-700">·</span>
                      {p.description}
                    </>
                  )}
                </div>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
