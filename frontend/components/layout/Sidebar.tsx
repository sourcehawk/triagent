"use client";

import { Suspense, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { usePathname, useSearchParams } from "next/navigation";
import { Bot } from "lucide-react";
import {
  api,
  ApiError,
  type Investigation,
  type PlaybookProposalListItem,
  type SyncState,
} from "@/lib/api";
import { wikiApi, type WikiProposalListItem } from "@/lib/wiki-api";
import { useStream } from "@/lib/stream";
import { useDialog } from "@/lib/dialog";
import { ConnectionsPanel } from "@/components/connections/ConnectionsPanel";
import { CheckIcon, EditIcon, UnsyncedIcon } from "@/components/shared/Icons";
import { LinkedReposPanel } from "@/components/repos/LinkedReposPanel";
import { PlaybookCorrelationPanel } from "@/components/playbooks/PlaybookCorrelationPanel";
import { Spinner } from "@/components/shared/Spinner";
import { RepoActivityPanel } from "@/components/repos/RepoActivityPanel";
import { extractOutgoing, findInverse } from "@/lib/playbook-relations";
import { getRecent } from "@/lib/recent-playbooks";
import { parsePlaybookYAML, type PlaybookListItem } from "@/lib/playbook";
import { labelFor, matchesQuery } from "@/lib/sidebar-label";
import { formatCostUSD, formatTokens, totalTokens } from "@/lib/usage";

type SidebarView = "investigations" | "watches" | "playbooks" | "mcp" | "wiki" | "repos";

type Props = {
  activeId: string | null;
  onSelect: (id: string) => void;
  onNew: () => void;
  // Optional secondary action specific to the playbooks view. When
  // set, a "+ new type" button renders next to "+ new playbook" so
  // type creation lives next to playbook creation rather than
  // hidden inside the main column.
  onNewType?: () => void;
  // Bumped by the parent whenever something changes that should refresh
  // the list (new preflight succeeded, deletion, …). Cheaper than wiring
  // an event bus.
  refreshNonce?: number;
  // Top-level view the sidebar is rendering. Optional — when omitted,
  // it is derived from the current pathname via `sidebarViewFromPath`.
  // Tests and future special-case callers can pass it explicitly.
  view?: SidebarView;
};

type InvRowProps = {
  inv: Investigation;
  active: boolean;
  onSelect: (id: string) => void;
  onDelete: (id: string, ev: React.MouseEvent) => void;
  onRenamed: (updated: Investigation) => void;
};

function InvRow({ inv, active, onSelect, onDelete, onRenamed }: InvRowProps) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(inv.label ?? "");
  const [saving, setSaving] = useState(false);
  const lbl = labelFor(inv);
  const dialog = useDialog();

  function startEdit(e: React.MouseEvent) {
    e.stopPropagation();
    setDraft(inv.label ?? "");
    setEditing(true);
  }

  async function commit() {
    const trimmed = draft.trim();
    if (!trimmed) {
      setEditing(false);
      return;
    }
    if (trimmed === (inv.label ?? "").trim()) {
      setEditing(false);
      return;
    }
    setSaving(true);
    try {
      const updated = await api.setInvestigationLabel(inv.id, trimmed);
      onRenamed(updated);
      setEditing(false);
    } catch (err) {
      // Stay in edit mode so the operator can fix and retry; surface
      // the failure as a toast so blur-triggered failures don't get
      // swallowed silently.
      const detail =
        err instanceof ApiError
          ? err.message
          : err instanceof Error
            ? err.message
            : String(err);
      dialog.notify({
        kind: "error",
        title: "Rename failed",
        body: detail,
        ttlMs: 6000,
      });
    } finally {
      setSaving(false);
    }
  }

  return (
    <li>
      {/* role=button (not a <button>) because the row hosts nested
          controls — rename / delete buttons and the rename <input> — and
          a <button> may not contain interactive descendants (invalid HTML
          + hydration error). tabIndex + onKeyDown keep keyboard activation;
          the guard restricts it to the row's own focus so Enter in the
          rename input doesn't also select the row. */}
      <div
        role="button"
        tabIndex={0}
        data-testid="triagent-investigation-row"
        data-investigation-id={inv.id}
        onClick={() => onSelect(inv.id)}
        onKeyDown={(e) => {
          if (e.target !== e.currentTarget) return;
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            onSelect(inv.id);
          }
        }}
        className={
          "group flex w-full cursor-pointer flex-col items-start gap-0.5 rounded px-2 py-1.5 text-left transition " +
          (active ? "bg-zinc-800/80" : "hover:bg-zinc-900")
        }
      >
        <div className="flex w-full items-baseline justify-between gap-2">
          {editing ? (
            <input
              autoFocus
              type="text"
              maxLength={80}
              value={draft}
              disabled={saving}
              onChange={(e) => setDraft(e.target.value)}
              onClick={(e) => e.stopPropagation()}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  void commit();
                } else if (e.key === "Escape") {
                  e.preventDefault();
                  setEditing(false);
                }
              }}
              onBlur={() => void commit()}
              className="min-w-0 flex-1 rounded border border-zinc-700 bg-zinc-950 px-1.5 py-0.5 text-sm text-zinc-100 focus:border-sky-600 focus:outline-none"
            />
          ) : (
            <span
              className={
                "flex min-w-0 flex-1 items-center gap-1.5 truncate text-sm group-hover:overflow-visible group-hover:whitespace-normal group-hover:break-words " +
                (lbl.placeholder ? "italic text-zinc-500" : "text-zinc-100")
              }
            >
              {inv.auto?.enabled && (
                // Pink while the auto-operator is actively driving
                // (started / resumed), zinc when paused (operator has
                // the wheel), emerald when the run finished cleanly,
                // and red on abort. This colour map mirrors the
                // SessionView header chip so the sidebar and the
                // session header agree at a glance.
                <Bot
                  data-testid="auto-glyph"
                  aria-label={`auto mode ${inv.auto.phase ?? "enabled"}`}
                  size={14}
                  className={
                    "shrink-0 " +
                    (inv.auto.phase === "started" || inv.auto.phase === "resumed"
                      ? "text-pink-500"
                      : inv.auto.phase === "paused"
                        ? "text-zinc-400"
                        : inv.auto.phase === "finished"
                          ? "text-emerald-500"
                          : inv.auto.phase === "aborted"
                            ? "text-red-500"
                            : "text-pink-500")
                  }
                />
              )}
              <span className="min-w-0 flex-1 truncate group-hover:overflow-visible group-hover:whitespace-normal group-hover:break-words">
                {lbl.text}
              </span>
            </span>
          )}
          <SidebarSyncIcon syncState={inv.syncState} />
          {!editing && (
            <button
              type="button"
              onClick={startEdit}
              aria-label="rename investigation"
              className="shrink-0 text-xs text-zinc-600 opacity-0 transition group-hover:opacity-100 hover:text-sky-400"
            >
              <EditIcon className="h-3.5 w-3.5" />
            </button>
          )}
          {!editing && (
            <button
              type="button"
              onClick={(e) => onDelete(inv.id, e)}
              aria-label="delete investigation"
              className="shrink-0 text-xs text-zinc-600 opacity-0 transition group-hover:opacity-100 hover:text-red-400"
            >
              ✕
            </button>
          )}
        </div>
        <StatusLine inv={inv} />
      </div>
    </li>
  );
}

export function Sidebar(props: Props) {
  return (
    <Suspense fallback={null}>
      <SidebarInner {...props} />
    </Suspense>
  );
}

function SidebarInner({
  activeId,
  onSelect,
  onNew,
  onNewType,
  refreshNonce,
  view: viewProp,
}: Props) {
  const pathname = usePathname() ?? "/";
  const view = viewProp ?? sidebarViewFromPath(pathname);
  const [items, setItems] = useState<Investigation[] | null>(null);
  const [filter, setFilter] = useState("");
  const filteredItems = useMemo(
    () => items?.filter((inv) => matchesQuery(inv, filter)) ?? null,
    [items, filter],
  );
  const [error, setError] = useState<string | null>(null);
  // Playbook list — fetched only when the sidebar is in the playbooks
  // view. Feeds the Related tray's outgoing/inverse computations.
  const [playbooks, setPlaybooks] = useState<PlaybookListItem[]>([]);
  useEffect(() => {
    if (view !== "playbooks") return;
    let cancelled = false;
    const refetch = () => {
      api
        .listPlaybooks()
        .then((list) => {
          if (!cancelled) setPlaybooks(list);
        })
        .catch(() => {
          /* Silently ignore — Related tray degrades gracefully when
             the list is empty, and the main playbook grid surfaces
             errors. */
        });
    };
    refetch();
    const onChange = () => refetch();
    window.addEventListener("c1:playbooks-changed", onChange);
    return () => {
      cancelled = true;
      window.removeEventListener("c1:playbooks-changed", onChange);
    };
  }, [view, refreshNonce]);

  // Derive current playbook id from the ?playbook= search param.
  const searchParams = useSearchParams();
  const currentID = useMemo(() => {
    if (view !== "playbooks") return null;
    const fromQuery = searchParams?.get("playbook");
    if (!fromQuery) return null;
    if (fromQuery === "__new" || fromQuery === "_") return null;
    return fromQuery;
  }, [view, searchParams]);

  // Derive the focused repo from ?repo=owner/name when in the repos
  // view. The codefix activity panel scopes itself to a single repo
  // when this is set, and shows the cross-repo list otherwise.
  const focusedRepo = useMemo(() => {
    if (view !== "repos") return null;
    const fromQuery = searchParams?.get("repo");
    if (!fromQuery) return null;
    const trimmed = fromQuery.trim();
    return trimmed.includes("/") ? trimmed : null;
  }, [view, searchParams]);

  // Subscribe to the same-tab CustomEvent fired by recent-playbooks.ts
  // so the Related tray refreshes the moment the operator visits a new
  // playbook (rather than waiting for a full sidebar re-render).
  const [recent, setRecent] = useState<string[]>([]);
  useEffect(() => {
    if (view !== "playbooks") return;
    setRecent(getRecent());
    const handler = () => setRecent(getRecent());
    window.addEventListener("triagent:recent-playbooks-changed", handler);
    return () => window.removeEventListener("triagent:recent-playbooks-changed", handler);
  }, [view]);

  // Outgoing links from the current playbook (computed from the loaded
  // list's yaml fields — no extra fetch). Empty when not on a playbook
  // route or when the current playbook can't be parsed.
  const outgoing = useMemo(() => {
    if (!currentID) return { delegates: [], handoffs: [] };
    const item = playbooks.find((p) => p.id === currentID);
    if (!item || !item.yaml) return { delegates: [], handoffs: [] };
    try {
      return extractOutgoing(parsePlaybookYAML(item.yaml));
    } catch {
      return { delegates: [], handoffs: [] };
    }
  }, [currentID, playbooks]);

  const inverse = useMemo(() => {
    if (!currentID) return { delegatedFrom: [], handoffsFrom: [] };
    return findInverse(playbooks, currentID);
  }, [currentID, playbooks]);

  // Recent: drop the current id (already shown in the editor) and ids
  // that no longer exist on disk (deleted between visits).
  const idSet = useMemo(() => new Set(playbooks.map((p) => p.id)), [playbooks]);
  const recentVisible = useMemo(
    () => recent.filter((id) => id !== currentID && idSet.has(id)),
    [recent, currentID, idSet],
  );

  const hasAnyRelated =
    recentVisible.length > 0 ||
    outgoing.delegates.length > 0 ||
    outgoing.handoffs.length > 0 ||
    inverse.delegatedFrom.length > 0 ||
    inverse.handoffsFrom.length > 0;
  // Drag-drop count: tracks dragenter/leave so the overlay only shows while
  // a file is genuinely hovering. Counter (rather than a boolean) handles
  // the dragenter/leave events fired for child elements as the cursor
  // moves through nested DOM.
  const [dragDepth, setDragDepth] = useState(0);
  const [importing, setImporting] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const dialog = useDialog();

  useEffect(() => {
    let cancelled = false;
    api
      .listInvestigations()
      .then((list) => {
        if (cancelled) return;
        setItems(list);
        setError(null);
      })
      .catch((e) => {
        if (cancelled) return;
        setError(e instanceof ApiError ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [refreshNonce]);

  // Listen for investigation-changed events. SessionWorkspace dispatches
  // these on label updates; InvestigationPageClient dispatches on first
  // load of a session view (covers newly-created investigations that
  // weren't in the sidebar's initial fetch). Upserts in-place so the
  // sidebar reflects both shape changes (new) and value changes (label,
  // status, etc.) without a full /api/investigations refetch.
  useEffect(() => {
    const handler = (e: Event) => {
      const updated = (e as CustomEvent<Investigation>).detail;
      if (!updated) return;
      setItems((prev) => {
        if (!prev) return prev;
        const idx = prev.findIndex((i) => i.id === updated.id);
        if (idx >= 0) {
          return prev.map((i) => (i.id === updated.id ? updated : i));
        }
        // Unknown id — prepend so newly-created investigations show up
        // at the top of the list (matches the sort key, newest first).
        return [updated, ...prev];
      });
    };
    window.addEventListener("triagent:investigation-changed", handler);
    return () => window.removeEventListener("triagent:investigation-changed", handler);
  }, []);

  // Live-update the streaming/idle pill on every row from the multiplex
  // stream. Without this the sidebar only refreshes when the operator
  // opens that specific session — auto-mode investigations triggered by a
  // watch never light up as "streaming" in the list because nobody opens
  // them. `end` envelopes are how the server signals turn completion;
  // any other investigation-scoped envelope means a turn is in flight.
  const stream = useStream();
  useEffect(() => {
    return stream.subscribe({ scope: "anyInvestigation" }, (env) => {
      const id = env.investigationId;
      if (!id) return;
      setItems((prev) => {
        if (!prev) return prev;
        const idx = prev.findIndex((i) => i.id === id);
        if (idx < 0) return prev;
        const cur = prev[idx];
        const streaming = env.kind !== "end";
        // started flips to true on the first envelope we see — covers
        // auto-mode investigations that begin streaming before the
        // sidebar's /api/investigations response set `started: true`.
        const started = cur.started || streaming;
        // Fold result-envelope usage + cost into the per-row running
        // total so the readout ticks without waiting for a full
        // /api/investigations refresh. Server-side aggregation in
        // investigate/internal/server/manager.go's publish() applies
        // the exact same delta on persisted state.
        let usage = cur.usage;
        let costUsd = cur.costUsd;
        if (env.kind === "result" && (env.usage || env.costUsd)) {
          const u = env.usage ?? {};
          const base = cur.usage ?? {};
          usage = {
            inputTokens: (base.inputTokens ?? 0) + (u.inputTokens ?? 0),
            outputTokens: (base.outputTokens ?? 0) + (u.outputTokens ?? 0),
            cacheCreationInputTokens:
              (base.cacheCreationInputTokens ?? 0) +
              (u.cacheCreationInputTokens ?? 0),
            cacheReadInputTokens:
              (base.cacheReadInputTokens ?? 0) + (u.cacheReadInputTokens ?? 0),
          };
          costUsd = (cur.costUsd ?? 0) + (env.costUsd ?? 0);
        }
        if (
          cur.streaming === streaming &&
          cur.started === started &&
          cur.usage === usage &&
          cur.costUsd === costUsd
        ) {
          return prev;
        }
        const next = prev.slice();
        next[idx] = { ...cur, streaming, started, usage, costUsd };
        return next;
      });
    });
  }, [stream]);

  async function importBundle(file: File) {
    if (importing) return;
    setImporting(true);
    try {
      const inv = await api.importInvestigation(file);
      // Optimistically prepend so the new card appears immediately, then
      // navigate. The parent's openExisting flow will refresh canonical
      // state via getInvestigation.
      setItems((prev) => (prev ? [inv, ...prev] : [inv]));
      onSelect(inv.id);
    } catch (err) {
      await dialog.alert({
        title: "Import failed",
        body: err instanceof ApiError ? err.message : String(err),
      });
    } finally {
      setImporting(false);
    }
  }

  function onFilePicked(e: React.ChangeEvent<HTMLInputElement>) {
    const f = e.target.files?.[0];
    // Reset value so picking the same file twice in a row still fires.
    e.target.value = "";
    if (f) void importBundle(f);
  }

  function onDrop(e: React.DragEvent<HTMLElement>) {
    e.preventDefault();
    setDragDepth(0);
    const f = e.dataTransfer.files?.[0];
    if (f) void importBundle(f);
  }

  async function remove(id: string, e: React.MouseEvent) {
    e.stopPropagation();
    const ok = await dialog.confirm({
      title: "Delete this investigation?",
      body: "The transcript and any saved port-forward state will be removed from disk.",
      confirmLabel: "Delete",
      danger: true,
    });
    if (!ok) return;
    try {
      await api.deleteInvestigation(id);
      setItems((prev) => prev?.filter((i) => i.id !== id) ?? prev);
      if (activeId === id) onNew();
    } catch (err) {
      await dialog.alert({
        title: "Delete failed",
        body: err instanceof ApiError ? err.message : String(err),
      });
    }
  }

  const activeInvestigation =
    items?.find((i) => i.id === activeId) ?? null;

  // Drag-drop is only meaningful in the investigations view (the only one
  // with importable bundles), so attach the handlers conditionally.
  const dropProps =
    view === "investigations"
      ? {
          onDragEnter: (e: React.DragEvent<HTMLElement>) => {
            // Only react to drags carrying files, so dragging text inside
            // the sidebar (e.g. selecting label text) doesn't trigger.
            if (!e.dataTransfer.types.includes("Files")) return;
            e.preventDefault();
            setDragDepth((n) => n + 1);
          },
          onDragOver: (e: React.DragEvent<HTMLElement>) => {
            if (!e.dataTransfer.types.includes("Files")) return;
            e.preventDefault();
            e.dataTransfer.dropEffect = "copy";
          },
          onDragLeave: (e: React.DragEvent<HTMLElement>) => {
            if (!e.dataTransfer.types.includes("Files")) return;
            setDragDepth((n) => Math.max(0, n - 1));
          },
          onDrop,
        }
      : {};

  return (
    <aside
      {...dropProps}
      className="relative flex w-64 shrink-0 flex-col border-r border-zinc-800 bg-zinc-950"
    >
      {view !== "mcp" && (
        <div className="flex flex-col gap-2 px-3 pt-4 pb-3">
          {view === "investigations" ? (
            <Link
              href="/investigations/new"
              data-testid="triagent-new-investigation"
              className="rounded bg-zinc-100 px-3 py-2 text-sm font-medium text-zinc-900 transition hover:bg-white"
            >
              + new investigation
            </Link>
          ) : view === "watches" ? (
            <Link
              href="/watches/new"
              className="rounded bg-zinc-100 px-3 py-2 text-sm font-medium text-zinc-900 transition hover:bg-white"
            >
              + new watch
            </Link>
          ) : (
            <button
              type="button"
              onClick={onNew}
              data-testid={
                view === "playbooks" ? "triagent-new-playbook" : undefined
              }
              className="rounded bg-zinc-100 px-3 py-2 text-sm font-medium text-zinc-900 transition hover:bg-white"
            >
              {view === "wiki"
                ? "+ new wiki entry"
                : view === "repos"
                  ? "+ add repository"
                  : "+ new playbook"}
            </button>
          )}
          {view === "investigations" && (
            <>
              <button
                type="button"
                onClick={() => fileInputRef.current?.click()}
                disabled={importing}
                title="upload a .triagent.json bundle a teammate shared with you"
                className="rounded border border-zinc-700 px-3 py-1.5 text-xs text-zinc-200 transition hover:border-zinc-500 hover:text-white disabled:cursor-not-allowed disabled:opacity-60"
              >
                {importing ? "importing…" : "↥ import investigation"}
              </button>
              <input
                ref={fileInputRef}
                type="file"
                accept=".json,.triagent.json,application/json"
                hidden
                onChange={onFilePicked}
              />
            </>
          )}
          {view === "playbooks" && onNewType && (
            <button
              type="button"
              onClick={onNewType}
              className="rounded border border-zinc-700 px-3 py-1.5 text-xs text-zinc-200 transition hover:border-zinc-500 hover:text-white"
            >
              + new type
            </button>
          )}
        </div>
      )}
      {dragDepth > 0 && (
        <div className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center rounded-sm border-2 border-dashed border-sky-500/60 bg-sky-950/40 backdrop-blur-[1px]">
          <span className="rounded bg-sky-900/80 px-3 py-1.5 text-xs font-medium text-sky-100">
            drop bundle to import
          </span>
        </div>
      )}

      <div className="flex-1 overflow-y-auto px-3 pt-3">
        {view === "investigations" ? (
          <>
            <div className="mb-2 flex items-center justify-between gap-2 px-1">
              <span className="text-xs uppercase tracking-wide text-zinc-500">history</span>
            </div>
            <input
              type="search"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="filter…"
              className="mb-2 w-full rounded border border-zinc-800 bg-zinc-950 px-2 py-1 text-xs text-zinc-200 placeholder:text-zinc-600 focus:border-sky-600 focus:outline-none"
            />
            {items === null && !error && (
              <div className="flex items-center gap-2 px-1 text-xs text-zinc-500">
                <Spinner className="h-3 w-3" /> loading…
              </div>
            )}
            {error && (
              <div className="rounded border border-red-900/60 bg-red-950/40 px-2 py-1.5 text-xs text-red-200/80">
                {error}
              </div>
            )}
            {filteredItems && filteredItems.length === 0 && items && items.length > 0 && (
              <div className="px-1 text-xs text-zinc-500">no investigations match &ldquo;{filter}&rdquo;</div>
            )}
            {items && items.length === 0 && (
              <div className="px-1 text-xs text-zinc-500">
                no past investigations
              </div>
            )}
            <ul className="space-y-1">
              {filteredItems?.map((inv) => (
                <InvRow
                  key={inv.id}
                  inv={inv}
                  active={inv.id === activeId}
                  onSelect={onSelect}
                  onDelete={remove}
                  onRenamed={(updated) =>
                    setItems((prev) => prev?.map((i) => (i.id === updated.id ? updated : i)) ?? prev)
                  }
                />
              ))}
            </ul>
          </>
        ) : view === "playbooks" ? (
          // Playbook view: the list of playbooks lives in the main pane
          // (it's wide and grid-laid-out), so the sidebar gives a brief
          // hint here rather than duplicating the list. When the operator
          // is in the editor for a specific playbook, the Related tray
          // surfaces recent visits + outgoing/inverse links.
          <div className="space-y-0">
            <PlaybookPendingProposals refreshNonce={refreshNonce} />
            {hasAnyRelated && (
              <RelatedSection
                recent={recentVisible}
                outgoing={outgoing}
                inverse={inverse}
                onPick={onSelect}
              />
            )}
            {currentID && (
              <PlaybookCorrelationPanel
                playbookID={currentID}
                onSelect={onSelect}
              />
            )}
            <div className="space-y-2 px-1 py-2 text-xs text-zinc-500">
              <p className="break-words">
                Playbooks shape the triagent-strategies walker. The main pane lists
                everything available; click one to inspect or edit.
              </p>
              <p className="break-words">
                Saved edits land at{" "}
                <code className="break-all rounded bg-zinc-800 px-1 py-0.5 font-mono text-xs">
                  ~/.config/c1/plugins/investigate/playbooks/
                </code>{" "}
                and become visible to claude on the next investigation.
              </p>
            </div>
          </div>
        ) : view === "mcp" ? (
          // mcp view: standalone reference catalog, no per-item navigation
          // from the sidebar (the catalog is global and a single page).
          <div className="space-y-2 px-1 text-xs text-zinc-500">
            <p className="break-words">
              Browse every tool the triagent-mcp servers expose to claude during an
              investigation. Read-only reference — no investigation needs to
              be active.
            </p>
            <p className="break-words">
              The catalog is built once at launcher start by aggregating each
              MCP&apos;s tool specs in-process; rebuild the binary to pick up new
              tools.
            </p>
          </div>
        ) : view === "watches" ? (
          // watches view: short blurb explaining what a watch is and how
          // the two toggles relate. The main pane lists watches + a
          // merged recent-signals feed.
          <div className="space-y-2 px-1 text-xs text-zinc-500">
            <p className="break-words">
              Persistent eyes-on a source — a GitHub issues query or a Slack
              channel — that polls on a schedule and surfaces new items as
              <span className="text-zinc-300"> signals</span>.
            </p>
            <p className="break-words">
              Flip <span className="text-zinc-300">auto-ingest</span> on to
              run the ingestion agent each poll for rich, classified
              signals. Flip <span className="text-zinc-300">auto-start</span>
              {" "}on to let the agent's recommendations spawn investigations
              automatically; otherwise they land as{" "}
              <span className="text-zinc-300">proposed</span> signals you
              approve from the card.
            </p>
          </div>
        ) : view === "repos" ? (
          // repos view: the main pane lists every linked repo with
          // its summary status; the sidenav surfaces repository
          // activity (issues + PRs the agent opened during
          // investigations). When the operator has focused a
          // specific repo via ?repo=owner/name, the panel scopes
          // itself to that repo.
          <RepoActivityPanel
            repo={focusedRepo}
            refreshNonce={refreshNonce}
          />
        ) : view === "wiki" ? (
          // wiki view: surface pending proposals first so an operator who
          // tabbed away from a proposal can re-open it, then a short
          // description of what the wiki is.
          <div className="space-y-3">
            <WikiPendingProposals refreshNonce={refreshNonce} />
            <div className="space-y-2 px-1 text-xs text-zinc-500">
              <p className="break-words">
                Promoted incident post-mortems and entity knowledge base. The
                main pane lists incidents and entities; the investigation
                agent can read from and write to this vault.
              </p>
            </div>
          </div>
        ) : null}
      </div>
      <LinkedReposPanel
        investigation={activeInvestigation}
        refreshNonce={refreshNonce}
      />
      <ConnectionsPanel refreshNonce={refreshNonce} />
    </aside>
  );
}

// SidebarSyncIcon renders the per-session sync indicator from the
// resolver's authoritative SyncState. Encapsulating the icon-mapping
// here (rather than inlining the conditional) means the upstream-list
// pill, the editor badge, and any future sync-aware view all read
// from the same status enum and stay in lockstep visually.
//
// Status → glyph map:
//   - synced     → CheckIcon (violet for merged PR, emerald otherwise)
//   - closed     → UnsyncedIcon (operator must re-push; the closed PR
//                  doesn't represent the session being on main)
//   - local-only → UnsyncedIcon (never pushed)
//   - unknown    → UnsyncedIcon (degraded state; better than a
//                  potentially stale check)
//   - upstream-only is not reachable for sidebar entries (sidebar
//     only renders local Investigations) but we render the unsynced
//     glyph for it defensively.
function SidebarSyncIcon({ syncState }: { syncState: SyncState }) {
  if (syncState.status === "synced") {
    const merged = syncState.pr?.state === "merged";
    return (
      <CheckIcon
        className={
          "h-3.5 w-3.5 shrink-0 " +
          (merged ? "text-violet-400" : "text-emerald-500")
        }
        aria-label={merged ? "PR merged" : "on upstream"}
      />
    );
  }
  return (
    <UnsyncedIcon
      className="h-3.5 w-3.5 shrink-0 text-zinc-500"
      aria-label={syncState.reason ?? "not on upstream"}
    />
  );
}

function StatusLine({ inv }: { inv: Investigation }) {
  let label = "ready";
  let cls = "text-zinc-500";
  if (inv.archived) {
    label = "archived";
    cls = "text-zinc-600";
  } else if (inv.streaming) {
    label = "streaming";
    cls = "text-sky-400";
  } else if (inv.started) {
    label = "idle";
    cls = "text-emerald-400";
  }
  const tokens = totalTokens(inv.usage);
  const cost = inv.costUsd ?? 0;
  const showUsage = tokens > 0 || cost > 0;
  return (
    <div className="flex w-full items-center gap-2 text-xs">
      <span className={cls}>{label}</span>
      <span className="text-zinc-700">·</span>
      <time className="text-zinc-600" dateTime={inv.createdAt}>
        {formatRelative(inv.createdAt)}
      </time>
      {inv.namespace && (
        <>
          <span className="text-zinc-700">·</span>
          <span className="truncate font-mono text-zinc-600 group-hover:overflow-visible group-hover:whitespace-normal group-hover:break-all">
            {inv.namespace}
          </span>
        </>
      )}
      {showUsage && (
        <span
          className="ml-auto shrink-0 font-mono text-zinc-500"
          title={`${formatTokens(tokens)} tokens · ${formatCostUSD(cost)} spent this session`}
        >
          {formatTokens(tokens)} tok · {formatCostUSD(cost)}
        </span>
      )}
    </div>
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

function sidebarViewFromPath(pathname: string): SidebarView {
  if (pathname.startsWith("/wiki")) return "wiki";
  if (pathname.startsWith("/watches")) return "watches";
  if (pathname.startsWith("/playbooks")) return "playbooks";
  if (pathname.startsWith("/repos")) return "repos";
  if (pathname.startsWith("/mcp")) return "mcp";
  if (pathname.startsWith("/investigations")) return "investigations";
  return "investigations";
}

function RelatedSection({
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
function WikiPendingProposals({
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
function PlaybookPendingProposals({
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
