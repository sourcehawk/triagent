"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  api,
  ApiError,
  type PlaybookListItem,
  type PlaybookTypeItem,
  type PlaybooksUpstreamStatus,
} from "@/lib/api";
import { useDialog } from "@/lib/dialog";
import {
  dumpPlaybookYAML,
  parsePlaybookYAML,
} from "@/lib/playbook";
import {
  CopyIcon,
  ExternalLinkIcon,
  GitHubIcon,
  SyncIcon,
  UnsyncedIcon,
  WarningIcon,
} from "@/components/shared/Icons";
import { Spinner } from "@/components/shared/Spinner";
import { DeleteTypeModal } from "@/components/playbooks/DeleteTypeModal";

type Props = {
  onOpen: (id: string) => void;
  onNew: () => void;
  // Bumped by the parent after a Save / Delete so the list refetches.
  refreshNonce?: number;
  // Bumped by the parent after a copy so the sidebar / list pick up
  // the new entry. Optional — if absent, the list falls back to a
  // local refetch on copy.
  onMutated?: () => void;
};

// TypeFilter is "all" or a concrete type name from the upstream
// repo. We resolve the canonical list dynamically (so renaming
// "general" → "system" upstream just shows up as a new tab) rather
// than baking type names into the frontend.
type TypeFilter = string;

// Cap how many cards render per page. Keeps the list-with-chrome
// (header + type tabs + search bar + upstream footer + 8 cards) inside
// a typical laptop viewport without scrolling; pagination handles the
// overflow.
const PAGE_SIZE = 8;

// State of the playbooks-upstream sync button (the per-list "pull
// origin" affordance), distinct from the per-playbook SyncState that
// comes off PlaybookListItem. Kept module-private; renamed in stage 1
// so a reader of `p.syncState.status` (per-playbook drift) and
// `setSyncState({kind: "syncing"})` (button state) doesn't have to
// guess which is which.
type UpstreamRepoSyncState =
  | { kind: "idle" }
  | { kind: "syncing" }
  | { kind: "error"; message: string };

export function PlaybookList({ onOpen, onNew, refreshNonce, onMutated }: Props) {
  const dialog = useDialog();
  const [copying, setCopying] = useState<string | null>(null);
  const [items, setItems] = useState<PlaybookListItem[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [upstream, setUpstream] = useState<PlaybooksUpstreamStatus | null>(null);
  // Distinguish "haven't tried yet" from "tried and got nothing" so the
  // upstream footer can render a skeleton during the in-flight fetch
  // instead of leaving a hole in the layout for ~1s.
  const [upstreamLoaded, setUpstreamLoaded] = useState(false);
  const [upstreamSyncState, setUpstreamSyncState] = useState<UpstreamRepoSyncState>({ kind: "idle" });
  const [availableTypes, setAvailableTypes] = useState<PlaybookTypeItem[]>([]);
  // Default to "investigation" if it exists, otherwise the first
  // available type, otherwise "all". This stays useful when the
  // upstream repo renames or adds slots — the resolution falls
  // through cleanly instead of leaving the operator stuck on a tab
  // that no longer exists.
  const [typeFilter, setTypeFilter] = useState<TypeFilter>("investigation");
  // Narrows the filtered list to playbooks whose local copy diverges
  // from upstream (or isn't on it yet) — same predicate the per-card
  // cloud-up icon uses, so the toggle just hides everything without
  // that icon. Layered on top of the type-tab filter, not replacing
  // it: "all unsynced" is the natural combination via the all tab.
  const [unsyncedOnly, setUnsyncedOnly] = useState(false);
  // Pagination keeps the playbooks list to a single viewport — the
  // page-size cap (8) is set high enough to fit the common case
  // (operators usually have ≤8 in any one type slot) but low enough
  // that the surrounding chrome — header, type tabs, search bar,
  // upstream footer — never gets pushed off the page on a 13" laptop.
  const [page, setPage] = useState(0);
  // When non-null, the hover-✕ on a tracked type opens DeleteTypeModal.
  // Untracked types skip the modal and use a plain dialog.confirm.
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    api
      .listPlaybooks()
      .then((list) => {
        if (cancelled) return;
        setItems(list);
        setError(null);
      })
      .catch((e) => {
        if (cancelled) return;
        setError(e instanceof ApiError ? e.message : String(e));
      });
    api
      .getPlaybooksUpstream()
      .then((s) => {
        if (cancelled) return;
        setUpstream(s);
        setUpstreamLoaded(true);
      })
      .catch(() => {
        if (cancelled) return;
        setUpstream(null);
        setUpstreamLoaded(true);
      });
    api
      .listPlaybookTypes()
      .then((types) => {
        if (cancelled) return;
        setAvailableTypes(types);
      })
      .catch(() => {
        if (cancelled) return;
        setAvailableTypes([]);
      });
    return () => {
      cancelled = true;
    };
  }, [refreshNonce]);

  // If the operator's chosen filter no longer exists in any visible
  // tab, drop them onto the closest sensible default rather than
  // showing an empty list.
  //
  // "Visible tab" = types from the upstream catalog (availableTypes)
  // OR types that appear on any loaded playbook (items). The latter
  // is the bug-fix case: locked system metas live under a `system/`
  // type slot that's NOT in the upstream catalog (it's launcher-
  // bundled), and operator-created subdirs like `investigation-wip`
  // also don't show up in availableTypes. Without including the
  // items' own types here, clicking those tabs would set the filter
  // and the effect would immediately bounce it back to
  // "investigation".
  useEffect(() => {
    if (typeFilter === "all") return;
    if (items === null) return; // wait for the first list response
    const inCatalog = availableTypes.some((t) => t.name === typeFilter);
    const inItems = items.some((p) => (p.type || "investigation") === typeFilter);
    if (inCatalog || inItems) return;
    if (availableTypes.length === 0 && items.length === 0) return;
    const fallback =
      availableTypes.find((t) => t.name === "investigation")?.name ??
      availableTypes[0]?.name ??
      "all";
    setTypeFilter(fallback);
  }, [availableTypes, items, typeFilter]);

  const sync = useCallback(async () => {
    setUpstreamSyncState({ kind: "syncing" });
    try {
      await api.syncPlaybooksUpstream();
      // Re-fetch upstream + the list so the header stamp + cards
      // reflect the new HEAD without waiting for the next mount.
      const [list, status] = await Promise.all([
        api.listPlaybooks(),
        api.getPlaybooksUpstream(),
      ]);
      setItems(list);
      setUpstream(status);
      setUpstreamSyncState({ kind: "idle" });
    } catch (e) {
      setUpstreamSyncState({
        kind: "error",
        message: e instanceof ApiError ? e.message : String(e),
      });
    }
  }, []);

  // copyPlaybook: clone an existing playbook under a fresh id. Prompts
  // for the new id (validating against existing ids on every keystroke),
  // fetches the source YAML, swaps the id field + resets version to v1
  // + sets active true, writes via savePlaybook, and routes to the
  // editor for follow-up edits. The source playbook is untouched.
  const copyPlaybook = useCallback(
    async (source: PlaybookListItem) => {
      const existing = new Set((items ?? []).map((p) => p.id));
      const newID = await dialog.prompt({
        title: `Copy "${source.id}"`,
        body: `Pick an id for the new playbook. Saves under the same type slot ("${source.type ?? "investigation"}"); you can change the type later via the editor's type dropdown.`,
        placeholder: `${source.id}_copy`,
        confirmLabel: "Copy",
        validate: (v) => {
          const trimmed = v.trim();
          if (!trimmed) return "id is required";
          if (!/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/.test(trimmed)) {
            return "id must start alphanumeric and contain only letters / digits / underscore / hyphen (max 64 chars)";
          }
          if (existing.has(trimmed)) {
            return `id "${trimmed}" already exists — pick another`;
          }
          return null;
        },
      });
      if (!newID) return;
      const trimmed = newID.trim();
      setCopying(source.id);
      try {
        // Pull the source's YAML directly. getPlaybook returns the
        // active version's YAML which is exactly what the operator
        // would see in the editor — no surprises in what's copied.
        const item = await api.getPlaybook(source.id);
        let parsed;
        try {
          parsed = parsePlaybookYAML(item.yaml);
        } catch (e) {
          throw new Error(
            `could not parse source YAML: ${e instanceof Error ? e.message : String(e)}`,
          );
        }
        // Swap id and mark active. Git history replaces file-versioning
        // so there's no version field to reset.
        parsed.id = trimmed;
        parsed.active = true;
        const yaml = dumpPlaybookYAML(parsed);
        const errs = await api.savePlaybook(
          trimmed,
          yaml,
          source.type ?? "investigation",
          true,
        );
        if (errs && errs.length > 0) {
          await dialog.alert({
            title: "Copy failed validation",
            body: errs.join("\n"),
          });
          return;
        }
        if (onMutated) onMutated();
        onOpen(trimmed);
      } catch (e) {
        await dialog.alert({
          title: `Could not copy "${source.id}"`,
          body: e instanceof ApiError ? e.message : String(e),
        });
      } finally {
        setCopying(null);
      }
    },
    [items, dialog, onMutated, onOpen],
  );

  // deleteType is the entry point for the hover-✕ on empty type tabs.
  // For untracked (operator-only) types, a single confirm + DELETE is
  // enough — the type was never pushed, so a local delete is durable.
  // For tracked (origin/HEAD) types, defer to DeleteTypeModal which
  // offers the propose-as-PR option.
  const deleteType = useCallback(
    async (t: { name: string; tracked: boolean }) => {
      if (t.tracked) {
        setDeleteTarget(t.name);
        return;
      }
      const ok = await dialog.confirm({
        title: `Delete empty type "${t.name}"?`,
        body: `Removes the local ${t.name}/ dir and type.txt. This type was not pushed upstream, so the removal is durable.`,
        confirmLabel: "delete",
        danger: true,
      });
      if (!ok) return;
      try {
        await api.deletePlaybookType(t.name);
        if (onMutated) onMutated();
      } catch (e) {
        await dialog.alert({
          title: `Could not delete "${t.name}"`,
          body: e instanceof ApiError ? e.message : String(e),
        });
      }
    },
    [dialog, onMutated],
  );

  // Type filter narrows the buckets first; the search query then runs
  // against id / symptom / source within the chosen bucket. Empty
  // type tab "all" skips the type narrowing.
  // Reset to the first page when the filter inputs change. Without
  // this, an operator who paginates to page 3 of "all" and then
  // switches to a type tab with only 5 entries lands on an empty
  // page 3 and has to manually click back.
  useEffect(() => {
    setPage(0);
  }, [typeFilter, query, unsyncedOnly]);

  const filtered = useMemo(() => {
    if (!items) return null;
    const byType =
      typeFilter === "all" ? items : items.filter((p) => p.type === typeFilter);
    const byUnsynced = unsyncedOnly
      ? byType.filter(
          (p) =>
            p.syncState.status === "local-drift" ||
            p.syncState.status === "local-only",
        )
      : byType;
    const q = query.trim().toLowerCase();
    if (!q) return byUnsynced;
    return byUnsynced.filter(
      (p) =>
        p.id.toLowerCase().includes(q) ||
        (p.symptom?.toLowerCase().includes(q) ?? false) ||
        p.source.toLowerCase().includes(q),
    );
  }, [items, query, typeFilter, unsyncedOnly]);

  // Total count of unsynced playbooks across the whole list (ignoring
  // the type-tab and search-query filters) — drives the toggle's
  // count badge so the operator sees up-front whether there's anything
  // to filter to.
  const unsyncedTotal = useMemo(() => {
    if (!items) return 0;
    let n = 0;
    for (const p of items) {
      if (
        p.syncState.status === "local-drift" ||
        p.syncState.status === "local-only"
      ) {
        n++;
      }
    }
    return n;
  }, [items]);

  // Tabs derive from the available types (one per slot the operator
  // has) plus an "all" tab. Counts come from the loaded items.
  // We list a tab even when the upstream metadata didn't return that
  // type but a user playbook in the items uses it — so a fresh
  // type the operator just authored doesn't disappear behind a
  // missing tab.
  const typeTabs = useMemo(() => {
    const counts = new Map<string, number>();
    for (const p of items ?? []) {
      const t = p.type || "investigation";
      counts.set(t, (counts.get(t) ?? 0) + 1);
    }
    const trackedByName = new Map<string, boolean>();
    for (const t of availableTypes) {
      trackedByName.set(t.name, t.tracked);
      if (!counts.has(t.name)) counts.set(t.name, 0);
    }
    const tabs = Array.from(counts.entries())
      .map(([name, count]) => ({
        name,
        count,
        // Types absent from availableTypes (e.g. an operator-created
        // dir not yet picked up by listPlaybookTypes, or the locked
        // bundled `system` slot) default to tracked=false. The
        // hover-✕ doesn't render for non-empty tabs anyway, so the
        // worst case is "operator hits ✕ on an empty type the
        // server wouldn't actually find" — handled by the 404
        // surface in deleteType / DeleteTypeModal.
        tracked: trackedByName.get(name) ?? false,
      }))
      .sort((a, b) => a.name.localeCompare(b.name));
    return tabs;
  }, [items, availableTypes]);
  const totalCount = items?.length ?? 0;
  const filteredCount = filtered?.length ?? 0;
  const totalPages = Math.max(1, Math.ceil(filteredCount / PAGE_SIZE));
  // Clamp page to the last valid page if the filtered list shrank
  // below the current offset (e.g. a sync-pull just removed the
  // entries you were paginated onto). Doesn't trigger a setState
  // round-trip — just clamps the slice we render.
  const safePage = Math.min(page, totalPages - 1);
  const paginated = filtered
    ? filtered.slice(safePage * PAGE_SIZE, (safePage + 1) * PAGE_SIZE)
    : null;

  return (
    <section className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <header className="space-y-1">
          <h2 className="text-2xl font-semibold tracking-tight">Playbooks</h2>
        </header>
        <UpstreamFooter
          upstream={upstream}
          loaded={upstreamLoaded}
          syncState={upstreamSyncState}
          onSync={sync}
        />
      </div>

      {items === null && !error && (
        <div className="flex items-center gap-2 text-sm text-zinc-500">
          <Spinner /> loading playbooks…
        </div>
      )}
      {error && (
        <div className="rounded border border-red-900/60 bg-red-950/40 p-3 text-sm text-red-200/90">
          {error}
        </div>
      )}
      {items && items.length === 0 && (
        <div className="rounded border border-zinc-800 bg-zinc-900/30 p-4 text-sm text-zinc-500">
          No playbooks. Click <strong>+ new playbook</strong> to author one.
        </div>
      )}

      {items && items.length > 0 && (
        // Wrapper container scopes the search visually — header + input
        // sit on top, the grid of cards underneath, all framed by the
        // same border so it's obvious which set the search filters.
        <div className="rounded border border-zinc-800 bg-zinc-900/30 p-4">
          {/* Type-filter tabs above the search input. Counts are
              static after page load so each tab tells the operator
              up-front how many entries it'll surface. */}
          <div className="mb-2 flex flex-wrap items-center gap-1">
            {typeTabs.map((t) => {
              const isActive = typeFilter === t.name;
              const canDelete = t.count === 0;
              return (
                <span
                  key={t.name}
                  className="group relative inline-flex items-center"
                >
                  <div
                    role="button"
                    tabIndex={0}
                    onClick={() => setTypeFilter(t.name)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" || e.key === " ") {
                        e.preventDefault();
                        setTypeFilter(t.name);
                      }
                    }}
                    aria-pressed={isActive}
                    className={
                      "cursor-pointer rounded px-2.5 py-1 text-xs transition focus:outline-none focus:ring-1 focus:ring-sky-700/40 " +
                      (canDelete ? "pr-7 " : "") +
                      (isActive
                        ? "bg-zinc-100 text-zinc-900"
                        : "border border-zinc-800 text-zinc-400 hover:border-zinc-600 hover:text-zinc-200")
                    }
                  >
                    {t.name}{" "}
                    <span className="text-xs opacity-70">({t.count})</span>
                  </div>
                  {canDelete && (
                    <button
                      type="button"
                      onClick={(e) => {
                        e.stopPropagation();
                        void deleteType(t);
                      }}
                      title={`Delete empty type "${t.name}"`}
                      aria-label={`Delete empty type ${t.name}`}
                      className={
                        "absolute right-1 top-1/2 -translate-y-1/2 inline-flex h-4 w-4 items-center justify-center rounded text-zinc-500 opacity-0 transition focus:opacity-100 group-hover:opacity-100 " +
                        (isActive
                          ? "hover:bg-zinc-300 hover:text-red-600"
                          : "hover:bg-zinc-800 hover:text-red-300")
                      }
                    >
                      ✕
                    </button>
                  )}
                </span>
              );
            })}
            <button
              type="button"
              onClick={() => setTypeFilter("all")}
              aria-pressed={typeFilter === "all"}
              className={
                "rounded px-2.5 py-1 text-xs transition " +
                (typeFilter === "all"
                  ? "bg-zinc-100 text-zinc-900"
                  : "border border-zinc-800 text-zinc-400 hover:border-zinc-600 hover:text-zinc-200")
              }
            >
              all{" "}
              <span className="text-xs opacity-70">({totalCount})</span>
            </button>
            {/* Spacer pushes the unsynced toggle to the right so it
                reads as a separate dimension from the type tabs.
                Disabled when nothing is unsynced — keeps the toggle
                from appearing actionable when it would just empty
                the list. */}
            <span className="flex-1" />
            <button
              type="button"
              onClick={() => setUnsyncedOnly((v) => !v)}
              aria-pressed={unsyncedOnly}
              disabled={unsyncedTotal === 0 && !unsyncedOnly}
              title={
                unsyncedTotal === 0
                  ? "no playbooks differ from upstream"
                  : "show only playbooks whose local copy isn't on origin/HEAD yet"
              }
              className={
                "inline-flex items-center gap-1 rounded px-2.5 py-1 text-xs transition disabled:cursor-not-allowed disabled:opacity-50 " +
                (unsyncedOnly
                  ? "bg-amber-500/80 text-zinc-950 hover:bg-amber-400"
                  : "border border-zinc-800 text-zinc-400 hover:border-amber-700 hover:text-amber-300")
              }
            >
              <UnsyncedIcon className="h-3.5 w-3.5" />
              unsynced{" "}
              <span className="text-xs opacity-70">({unsyncedTotal})</span>
            </button>
          </div>
          <div className="mb-3 flex items-center gap-3">
            <input
              type="search"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="search by id, symptom, or source…"
              aria-label="search playbooks"
              className="flex-1 rounded border border-zinc-800 bg-zinc-950 px-3 py-1.5 text-sm text-zinc-100 placeholder-zinc-600 focus:border-zinc-600 focus:outline-none"
            />
            <span className="text-xs text-zinc-500">
              {filtered?.length ?? 0} / {items.length}
            </span>
          </div>

          {filtered && filtered.length === 0 ? (
            <div className="px-1 py-6 text-center text-sm text-zinc-500">
              {query ? (
                <>
                  No playbooks match{" "}
                  <span className="font-mono text-zinc-300">{query}</span>
                  {unsyncedOnly && " in the unsynced set"}.
                </>
              ) : unsyncedOnly ? (
                <>No unsynced playbooks in this type.</>
              ) : (
                <>No playbooks in this view.</>
              )}
            </div>
          ) : (
            <ul
              data-testid="triagent-playbook-list"
              className="grid grid-cols-1 gap-3 md:grid-cols-2"
            >
              {paginated?.map((p) => (
                <li
                  key={p.id}
                  data-testid="triagent-playbook-card"
                  data-playbook-id={p.id}
                  className="relative"
                >
                  {/* Card body — div with role=button so the in-card
                      copy button can sit as a sibling without nested
                      <button> (invalid HTML). Keyboard handler keeps
                      Enter/Space activation working for the
                      "click-the-card-to-open" gesture. */}
                  <div
                    role="button"
                    tabIndex={0}
                    onClick={() => onOpen(p.id)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" || e.key === " ") {
                        e.preventDefault();
                        onOpen(p.id);
                      }
                    }}
                    className="flex h-full w-full cursor-pointer flex-col items-start gap-2 rounded border border-zinc-800 bg-zinc-900/40 p-4 text-left transition hover:border-zinc-700 hover:bg-zinc-900/70 focus:border-sky-700/60 focus:outline-none focus:ring-1 focus:ring-sky-700/40"
                  >
                    <div className="flex w-full items-baseline justify-between gap-2">
                      <span className="flex items-center gap-1.5 truncate">
                        {(p.syncState.status === "local-drift" ||
                          p.syncState.status === "local-only") && (
                          // Cloud-with-up-arrow rather than the
                          // generic warning triangle — "unsynced"
                          // isn't an error, it's just "your local
                          // copy diverges from upstream / push
                          // pending". Tooltip text comes from the
                          // server's resolver so list and editor
                          // can't disagree about why something is
                          // flagged.
                          <span
                            title={
                              p.syncState.reason
                                ? `${p.syncState.reason} — push as PR via the editor to share.`
                                : "Unsynced local changes — push as PR via the editor to share."
                            }
                            className="inline-flex shrink-0 cursor-help text-zinc-500"
                          >
                            <UnsyncedIcon className="h-5 w-5" />
                          </span>
                        )}
                        <span className="truncate font-mono text-sm text-zinc-100">
                          {p.id}
                        </span>
                      </span>
                      {/* Right-aligned badges — pr to leave room for
                          the absolute-positioned copy button. */}
                      <div className="flex items-baseline gap-1.5 pr-7">
                        <TypeBadge type={p.type} />
                        <SourceBadge source={p.source} />
                        <StatusPill disabled={!!p.disabled} />
                        {p.schemaMismatch && (
                          <span
                            className="inline-flex items-center gap-1 rounded-full bg-amber-900/40 px-2 py-0.5 text-xs font-medium uppercase tracking-wide text-amber-300"
                            title={`schema v${p.schemaVersion ?? 1} — needs migration`}
                          >
                            <WarningIcon className="h-2.5 w-2.5" />
                            schema v{p.schemaVersion ?? 1}
                          </span>
                        )}
                      </div>
                    </div>
                    {p.symptom && (
                      <p className="line-clamp-3 text-xs text-zinc-400">
                        {p.symptom}
                      </p>
                    )}
                    <div className="mt-auto flex w-full flex-wrap items-baseline gap-x-3 gap-y-0.5 text-xs text-zinc-500">
                      <span>
                        {p.nodeCount} node{p.nodeCount === 1 ? "" : "s"}
                      </span>
                    </div>
                  </div>
                  {/* Copy button — absolute-positioned so it overlays
                      the card without being nested inside the
                      role=button div. stopPropagation prevents the
                      card click from firing alongside. */}
                  <button
                    type="button"
                    onClick={(e) => {
                      e.stopPropagation();
                      void copyPlaybook(p);
                    }}
                    disabled={copying === p.id}
                    title={`Copy "${p.id}" under a new id`}
                    aria-label={`Copy ${p.id}`}
                    className="absolute right-2 top-2 inline-flex h-6 w-6 items-center justify-center rounded text-zinc-500 transition hover:bg-zinc-800 hover:text-zinc-200 disabled:opacity-50"
                  >
                    {copying === p.id ? (
                      <Spinner className="h-3 w-3" />
                    ) : (
                      <CopyIcon className="h-3.5 w-3.5" />
                    )}
                  </button>
                </li>
              ))}
            </ul>
          )}
          {filteredCount > PAGE_SIZE && (
            <Pagination
              page={safePage}
              pageSize={PAGE_SIZE}
              total={filteredCount}
              totalPages={totalPages}
              onChange={setPage}
            />
          )}
        </div>
      )}
      {deleteTarget && (
        <DeleteTypeModal
          name={deleteTarget}
          onClose={() => setDeleteTarget(null)}
          onDeleted={() => {
            setDeleteTarget(null);
            if (onMutated) onMutated();
          }}
        />
      )}
    </section>
  );
}

// Pagination is a compact prev/next + "page X of Y" control rendered
// below the playbook grid. Hidden when the filtered count fits on a
// single page; otherwise sits flush against the cards above.
function Pagination({
  page,
  pageSize,
  total,
  totalPages,
  onChange,
}: {
  page: number;
  pageSize: number;
  total: number;
  totalPages: number;
  onChange: (next: number) => void;
}) {
  const start = page * pageSize + 1;
  const end = Math.min((page + 1) * pageSize, total);
  const canPrev = page > 0;
  const canNext = page < totalPages - 1;
  return (
    <div className="mt-3 flex items-center justify-between border-t border-zinc-800 pt-3 text-xs text-zinc-500">
      <span>
        showing <span className="text-zinc-300">{start}–{end}</span> of{" "}
        <span className="text-zinc-300">{total}</span>
      </span>
      <div className="flex items-center gap-1">
        <button
          type="button"
          onClick={() => onChange(page - 1)}
          disabled={!canPrev}
          aria-label="previous page"
          className="rounded border border-zinc-800 px-2 py-1 text-xs text-zinc-300 transition hover:border-zinc-600 hover:text-zinc-100 disabled:cursor-not-allowed disabled:border-zinc-900 disabled:text-zinc-700"
        >
          ‹ prev
        </button>
        <span className="px-2 font-mono text-xs text-zinc-400">
          {page + 1} / {totalPages}
        </span>
        <button
          type="button"
          onClick={() => onChange(page + 1)}
          disabled={!canNext}
          aria-label="next page"
          className="rounded border border-zinc-800 px-2 py-1 text-xs text-zinc-300 transition hover:border-zinc-600 hover:text-zinc-100 disabled:cursor-not-allowed disabled:border-zinc-900 disabled:text-zinc-700"
        >
          next ›
        </button>
      </div>
    </div>
  );
}

// UpstreamFooter is the three-div strip anchored to the right of the
// page header, mirroring the wiki layout. Layout (left → right):
//
//   1. last synced: <relative time>
//   2. synced from: <repo>  (clickable, opens GitHub)
//   3. [sync icon] sync   (badge with N when remote has commits ahead)
//
// Padded apart with a gap class so each pieces stays its own
// readable block instead of running together. Errors stack below.
function UpstreamFooter({
  upstream,
  loaded,
  syncState,
  onSync,
}: {
  upstream: PlaybooksUpstreamStatus | null;
  loaded: boolean;
  syncState: UpstreamRepoSyncState;
  onSync: () => void | Promise<void>;
}) {
  if (!loaded) {
    return <UpstreamFooterSkeleton />;
  }
  if (!upstream) {
    return null;
  }
  const canSync =
    upstream.gitCheckout && !!upstream.repo && syncState.kind !== "syncing";
  const repoUrl = upstream.repo
    ? `https://github.com/${upstream.repo}`
    : null;
  const remoteAhead = upstream.remoteAhead ?? 0;
  return (
    <div className="flex flex-col items-end gap-1">
      <div className="flex items-center gap-6 text-xs text-zinc-500">
        <span title={upstream.lastSynced || ""}>
          last synced:{" "}
          <span className="text-zinc-300">
            {upstream.lastSynced ? relativeTime(upstream.lastSynced) : "never"}
          </span>
        </span>
        {repoUrl ? (
          <a
            href={repoUrl}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1.5 text-zinc-400 transition hover:text-zinc-100"
          >
            <GitHubIcon className="h-3.5 w-3.5" />
            <span>
              synced from:{" "}
              <span className="font-mono text-zinc-300">{upstream.repo}</span>
            </span>
            <ExternalLinkIcon className="h-3 w-3 opacity-60" />
          </a>
        ) : (
          <span>
            synced from: <span className="font-mono">(unset)</span>
          </span>
        )}
        <button
          type="button"
          onClick={() => void onSync()}
          disabled={!canSync}
          title={
            !upstream.gitCheckout
              ? "playbooks dir is not a git checkout — sync needs a cloned repo"
              : !upstream.repo
                ? "no upstream repo configured"
                : remoteAhead > 0
                  ? `${remoteAhead} upstream commit${remoteAhead === 1 ? "" : "s"} not yet applied`
                  : "git fetch + reset --hard origin/HEAD"
          }
          className={
            "inline-flex items-center gap-1.5 rounded border px-2.5 py-1 text-xs transition " +
            (canSync
              ? "border-zinc-700 text-zinc-200 hover:border-zinc-500 hover:text-zinc-100"
              : "border-zinc-800 text-zinc-600")
          }
        >
          <SyncIcon
            className={
              "h-3.5 w-3.5 " +
              (syncState.kind === "syncing" ? "animate-spin" : "")
            }
          />
          <span>{syncState.kind === "syncing" ? "syncing…" : "sync"}</span>
          {remoteAhead > 0 && syncState.kind !== "syncing" && (
            <span className="ml-1 rounded-full bg-amber-500/80 px-1.5 py-0.5 text-xs font-semibold text-zinc-950">
              {remoteAhead}
            </span>
          )}
        </button>
      </div>
      {remoteAhead > 0 && syncState.kind !== "syncing" && (
        <div className="text-xs text-amber-400/80">
          upstream has {remoteAhead} commit{remoteAhead === 1 ? "" : "s"} not
          yet applied locally — click sync to pull.
        </div>
      )}
      {upstream.remoteAheadError && remoteAhead === 0 && (
        <div className="max-w-[40rem] truncate text-xs text-zinc-600">
          (couldn't check upstream: {upstream.remoteAheadError})
        </div>
      )}
      {syncState.kind === "error" && (
        <div className="max-w-[40rem] truncate text-xs text-red-400">
          {syncState.message}
        </div>
      )}
    </div>
  );
}

// UpstreamFooterSkeleton reserves the space the loaded footer
// occupies while the upstream-status fetch is in flight. Three
// pulsing placeholders matching the loaded layout's three slots
// (last synced, synced from, sync button) keep the surrounding
// chrome from jumping when the real footer lands ~1s after page
// load.
function UpstreamFooterSkeleton() {
  return (
    <div
      className="flex flex-col items-end gap-1"
      aria-busy
      aria-label="loading github sync status"
    >
      <div className="flex items-center gap-6">
        <div className="h-4 w-28 animate-pulse rounded bg-zinc-800/60" />
        <div className="h-4 w-48 animate-pulse rounded bg-zinc-800/60" />
        <div className="h-6 w-16 animate-pulse rounded bg-zinc-800/60" />
      </div>
    </div>
  );
}

// relativeTime renders an ISO-8601 timestamp as "5m ago" / "2h ago"
// / "3d ago". Falls back to the raw string for parse failures so the
// operator still sees something useful.
function relativeTime(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const seconds = Math.floor((Date.now() - t) / 1000);
  if (seconds < 60) return `${seconds}s ago`;
  const m = Math.floor(seconds / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h}h ago`;
  const d = Math.floor(h / 24);
  return `${d}d ago`;
}

// SourceBadge answers "where does the active version live?":
// "remote" = upstream-only (the operator hasn't authored or
// overridden it locally), "local" = there's a user file (whether
// it's a fresh id or an override of an upstream one). Collapsed
// from the previous user/override split because the more useful
// distinction (does it match remote?) is carried by the unsynced
// warning icon next to the id.
function SourceBadge({ source }: { source: PlaybookListItem["source"] }) {
  const { label, cls } = sourceBadgeStyle(source);
  return (
    <span
      className={
        "rounded-full px-2 py-0.5 text-xs font-medium uppercase tracking-wide " +
        cls
      }
      title={
        source === "plugin"
          ? "lives in the upstream playbooks repo"
          : source === "system"
            ? "ships bundled with the launcher (locked — non-overridable)"
            : source === "broken"
              ? "file on disk could not be parsed"
              : "lives in the local user playbooks dir (override or fresh id)"
      }
    >
      {label}
    </span>
  );
}

function sourceBadgeStyle(source: PlaybookListItem["source"]): {
  label: string;
  cls: string;
} {
  switch (source) {
    case "plugin":
      return { label: "remote", cls: "bg-zinc-800 text-zinc-300" };
    case "system":
      // Sky tone for system metas — visually distinct from "remote
      // upstream library" so the lock signal reads at a glance.
      return { label: "system", cls: "bg-sky-900/50 text-sky-200" };
    case "user":
    case "override":
      return { label: "local", cls: "bg-emerald-900/50 text-emerald-300" };
    case "broken":
      return { label: "broken", cls: "bg-red-900/60 text-red-300" };
  }
}

// StatusPill is the always-visible "active / disabled" affirmation.
// Lives next to the SourceBadge so every card carries one of two
// signals — no more reading the absence of a disabled badge as
// "active". Mirrors the editor's StatusBadge visually (switch-track
// + label) so the same playbook reads the same way in the list and
// in the open editor — only the editor variant is interactive.
function StatusPill({ disabled }: { disabled: boolean }) {
  return (
    <span
      aria-label={disabled ? "disabled" : "active"}
      className={
        "inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs font-medium uppercase tracking-wide " +
        (disabled
          ? "border-zinc-700 bg-zinc-800/60 text-zinc-400"
          : "border-emerald-700/60 bg-emerald-900/40 text-emerald-300")
      }
    >
      <span
        aria-hidden
        className={
          "relative inline-block h-2.5 w-5 rounded-full " +
          (disabled ? "bg-zinc-700" : "bg-emerald-500/70")
        }
      >
        <span
          className={
            "absolute top-[2px] h-1.5 w-1.5 rounded-full bg-zinc-100 " +
            (disabled ? "left-[2px]" : "left-[12px]")
          }
        />
      </span>
      <span className={disabled ? "line-through decoration-zinc-600" : ""}>
        {disabled ? "disabled" : "active"}
      </span>
    </span>
  );
}

// TypeBadge renders the playbook's type (the directory it lives in
// upstream — investigation, general, system, …). Always visible so
// every card carries its category at a glance, with the source badge
// next to it answering "remote vs local" separately. Colour-codes
// the canonical types so investigation stays subtle and other slots
// stand out, but unknown values render in a neutral pill so newly
// added types Just Work.
function TypeBadge({ type }: { type?: PlaybookListItem["type"] }) {
  const name = type || "investigation";
  const cls =
    name === "investigation"
      ? "bg-zinc-800 text-zinc-300"
      : name === "general"
        ? "bg-sky-900/50 text-sky-300"
        : "bg-indigo-900/50 text-indigo-300";
  return (
    <span
      className={
        "rounded-full px-2 py-0.5 text-xs font-medium uppercase tracking-wide " +
        cls
      }
      title={`type: ${name}`}
    >
      {name}
    </span>
  );
}


