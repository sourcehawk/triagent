"use client";

// WikiEditor is the wiki counterpart to PlaybookEditor — same shape
// (header + tabbed main pane + side panel) but with a different
// content model. The operator can edit the root incident plus its
// first-degree entity neighbours in a single session and push the
// whole bundle as one PR.
//
// State:
// - selection: which graph node the side panel + body tab are bound to
// - drafts: per-node edit slots, keyed by NodeKey ("incident:<id>" or
//   "entity:<type>:<name>"). An entry exists only after the operator
//   touches that node — empty drafts aren't created speculatively.
// - tab: "diagram" | "body". Body tab follows the active selection.
//
// Save runs PUT requests for every dirty draft sequentially (incidents
// before entities), surfacing per-file errors. Push-as-PR collects the
// dirty + locally-committed files and posts to /push-pr-bundle.

import type React from "react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import {
  wikiApi,
  ApiError,
  type WikiEntryDetail,
  type WikiEntityDetail,
  type WikiBundleFile,
} from "@/lib/wiki-api";
import { api, type Capabilities, type SyncState } from "@/lib/api";
import { useDialog } from "@/lib/dialog";
import { Spinner } from "@/components/shared/Spinner";
import {
  ArrowLeftIcon,
  ChatBubbleIcon,
  GitHubIcon,
  RevertIcon,
  UnsyncedIcon,
} from "@/components/shared/Icons";
import {
  BTN_BASE,
  BTN_GATED,
  BTN_PRIMARY,
  BTN_SECONDARY,
  BTN_SECONDARY_ACTIVE,
} from "@/lib/buttons";
import { WikiGraph, type WikiGraphSelection } from "./WikiGraph";
import {
  WikiNodeEditor,
  type IncidentDraftView,
  type EntityDraftView,
} from "./WikiNodeEditor";
import { WikiBodyTab } from "./WikiBodyTab";
import { PushWikiPRModal } from "./PushWikiPRModal";
import { EditorChatDrawer } from "@/components/playbooks/EditorChatDrawer";
import {
  WikiProposalCard,
  type WikiProposalPayload,
} from "@/components/wiki/WikiProposalCard";
import { type ProposalDraftPayload } from "@/components/playbooks/ProposalCard";
import { PROPOSE_WIKI_DRAFT_TOOL_NAME } from "@/lib/events";

// Parser for the wiki drawer. Validates the three required fields the
// editor's UI relies on, dedupes by proposal_id (re-drafts carry a
// fresh proposal_id so they naturally replace the prior single entry).
function parseWikiProposal(
  raw: unknown,
): { key: string; payload: WikiProposalPayload } | null {
  const r = raw as Partial<WikiProposalPayload>;
  if (
    typeof r?.proposal_id !== "string" ||
    r?.kind !== "wiki_proposal_draft" ||
    typeof r?.new_md !== "string"
  ) {
    return null;
  }
  // Wiki sessions don't multi-draft, but use proposal_id so re-drafts
  // (which carry a fresh proposal_id) replace any prior single entry.
  return { key: r.proposal_id, payload: r as WikiProposalPayload };
}

// FALLBACK_CAPABILITIES guards WikiProposalCard's approve action while
// the capabilities fetch is in flight — wiki + gh both unavailable, so
// approve disables itself with a sensible reason. Mirrors SessionView's
// pattern for the same component on the investigation surface.
const FALLBACK_CAPABILITIES: Capabilities = {
  gh: { installed: false, authenticated: false },
  repoPath: { configured: false, valid: false },
  wiki: { configured: false, valid: false },
  claude: { installed: false },
  sessions: { configured: false, valid: false },
};

// ── Selection / NodeKey ──────────────────────────────────────────────────────

export type WikiSelection =
  | { kind: "incident"; id: string }
  | { kind: "entity"; type: string; name: string };

function nodeKey(sel: WikiSelection): string {
  return sel.kind === "incident"
    ? `incident:${sel.id}`
    : `entity:${sel.type}:${sel.name}`;
}

// ── Drafts ──────────────────────────────────────────────────────────────────

type IncidentDraft = {
  kind: "incident";
  id: string;
  title: string;
  severity: string;
  body: string;
  // Frontmatter fields surfaced to the side panel as read-only badges.
  status?: string;
  date?: string;
  services?: string[];
  errors?: string[];
  symptoms?: string[];
  // syncState — the loaded value from GET. Drafts whose status is
  // "local-drift" land in the push-PR bundle; "synced" drafts are
  // skipped because pushing them would be a no-op. Mirrors the
  // resolver answer used by EntryRow.
  syncState: SyncState;
  original: { title: string; severity: string; body: string };
};

type EntityDraft = {
  kind: "entity";
  type: string;
  name: string;
  body: string;
  created?: string;
  syncState: SyncState;
  backlinks?: WikiEntityDetail["backlinks"];
  original: { body: string };
};

type Draft = IncidentDraft | EntityDraft;

function draftDirty(d: Draft): boolean {
  if (d.kind === "incident") {
    return (
      d.title !== d.original.title ||
      d.severity !== d.original.severity ||
      d.body !== d.original.body
    );
  }
  return d.body !== d.original.body;
}

// ── Props ───────────────────────────────────────────────────────────────────

export type WikiEditorProps =
  | { rootKind: "incident"; rootId: string }
  | { rootKind: "entity"; rootType: string; rootName: string };

// ── Component ───────────────────────────────────────────────────────────────

export function WikiEditor(props: WikiEditorProps) {
  const dialog = useDialog();

  const initialSelection: WikiSelection =
    props.rootKind === "incident"
      ? { kind: "incident", id: props.rootId }
      : { kind: "entity", type: props.rootType, name: props.rootName };

  // Deep-link params: ?proposal=<id>&tab=proposal lets the wiki sidenav
  // jump straight into the AI proposal tab for a still-pending draft.
  // useSearchParams is reactive — clicks within the editor that update
  // the URL won't remount the component, so we capture the initial
  // values on mount and don't re-react to changes.
  const searchParams = useSearchParams();
  const initialProposalID = searchParams?.get("proposal") ?? null;
  const initialTabHint = searchParams?.get("tab") ?? null;

  const [selection, setSelection] = useState<WikiSelection>(initialSelection);
  const [drafts, setDrafts] = useState<Map<string, Draft>>(new Map());
  const [tab, setTab] = useState<"diagram" | "body" | "proposal">("diagram");
  // Pending AI proposal for THIS wiki entry, lifted from the chat drawer
  // so the "AI proposal" tab can show the live preview. Cleared on
  // approve (via the c1:wiki-approved listener — same hook that refetches
  // the body) and on the tab's discard ✕. Mirrors PlaybookEditor's
  // pendingProposal pattern; the drawer fires onPendingProposalChange
  // whenever a propose_wiki_draft tool result lands in its transcript.
  const [pendingProposal, setPendingProposal] =
    useState<WikiProposalPayload | null>(null);
  // Dismissed proposal ids are handed back to the chat drawer so the chat
  // ProposalCard for each proposal hides its action row once we've
  // handled it via the tab.
  const [dismissedProposalIDs, setDismissedProposalIDs] = useState<Set<string>>(
    () => new Set(),
  );
  const dismissedProposalIDsRef = useRef(dismissedProposalIDs);
  useEffect(() => {
    dismissedProposalIDsRef.current = dismissedProposalIDs;
  }, [dismissedProposalIDs]);
  const [capabilities, setCapabilities] = useState<Capabilities | null>(null);
  const [rootError, setRootError] = useState<string | null>(null);
  const [loadingRoot, setLoadingRoot] = useState(true);
  const [loadingNode, setLoadingNode] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveErrors, setSaveErrors] = useState<Record<string, string[]>>({});
  const [showPushModal, setShowPushModal] = useState(false);
  const [resolving, setResolving] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  // Chat drawer state. hadChat tracks whether the operator has opened
  // chat at least once during this mount, so the "resume chat" floating
  // button only appears after they've explicitly engaged with chat.
  const [chatOpen, setChatOpen] = useState(false);
  const [hadChat, setHadChat] = useState(false);

  // Track which keys have been seen-on-disk vs. unknown. Used to gate
  // the body tab — entity references in a freshly-expanded incident
  // graph that don't have files yet must not open an empty editor.
  const [bodyAvailableKeys, setBodyAvailableKeys] = useState<Set<string>>(
    new Set(),
  );

  // Capabilities once.
  useEffect(() => {
    api
      .getCapabilities()
      .then(setCapabilities)
      .catch(() => setCapabilities(null));
  }, []);

  // ── Loaders ───────────────────────────────────────────────────────────────

  const loadIncidentDraft = useCallback(
    async (id: string): Promise<{ draft: IncidentDraft; detail: WikiEntryDetail }> => {
      const detail = await wikiApi.getEntry(id);
      const fm = detail.frontmatter;
      const draft: IncidentDraft = {
        kind: "incident",
        id,
        title: fm.title ?? "",
        severity: fm.severity ?? "",
        body: detail.markdown ?? "",
        status: fm.status,
        date: fm.date,
        services: fm.services ?? [],
        errors: fm.errors ?? [],
        symptoms: fm.symptoms ?? [],
        syncState: detail.syncState,
        original: {
          title: fm.title ?? "",
          severity: fm.severity ?? "",
          body: detail.markdown ?? "",
        },
      };
      return { draft, detail };
    },
    [],
  );

  const loadEntityDraft = useCallback(
    async (
      type: string,
      name: string,
    ): Promise<{ draft: EntityDraft; detail: WikiEntityDetail }> => {
      const detail = await wikiApi.getEntity(type, name);
      const draft: EntityDraft = {
        kind: "entity",
        type,
        name,
        body: detail.markdown ?? "",
        created: detail.frontmatter?.created,
        syncState: detail.syncState,
        backlinks: detail.backlinks ?? [],
        original: { body: detail.markdown ?? "" },
      };
      return { draft, detail };
    },
    [],
  );

  // Seed root on mount. The backend returns a synthetic empty entry
  // (is_stub: true) when the wiki file doesn't exist on disk yet, so we
  // never see a 404 here for the "freshly-created backfill" flow — the
  // editor mounts cleanly with empty drafts and we auto-open chat so the
  // operator can drive the agent toward the first draft.
  //
  // Exception: a ?proposal=<id> deep-link means the operator clicked a
  // pending-proposal shortcut in the sidebar. They came to review, not
  // to chat. Suppress the auto-open so (a) the chat drawer doesn't pop
  // in front of the proposal tab, and (b) its onPendingProposalsChange
  // doesn't clobber the deep-link's hydrated pendingProposal back to
  // null on first transcript-load.
  useEffect(() => {
    let cancelled = false;
    setLoadingRoot(true);
    setRootError(null);
    (async () => {
      try {
        let isStub = false;
        if (props.rootKind === "incident") {
          const { draft, detail } = await loadIncidentDraft(props.rootId);
          if (cancelled) return;
          const k = `incident:${props.rootId}`;
          setDrafts((prev) => {
            const next = new Map(prev);
            next.set(k, draft);
            return next;
          });
          setBodyAvailableKeys((prev) => new Set(prev).add(k));
          isStub = detail.is_stub === true;
        } else {
          const { draft, detail } = await loadEntityDraft(props.rootType, props.rootName);
          if (cancelled) return;
          const k = `entity:${props.rootType}:${props.rootName}`;
          setDrafts((prev) => {
            const next = new Map(prev);
            next.set(k, draft);
            return next;
          });
          setBodyAvailableKeys((prev) => new Set(prev).add(k));
          isStub = detail.is_stub === true;
        }
        if (isStub && !initialProposalID) {
          setChatOpen(true);
          setHadChat(true);
        }
      } catch (e) {
        if (cancelled) return;
        setRootError(e instanceof ApiError ? e.message : String(e));
      } finally {
        if (!cancelled) setLoadingRoot(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [
    props.rootKind,
    (props as { rootId?: string }).rootId,
    (props as { rootType?: string }).rootType,
    (props as { rootName?: string }).rootName,
    loadIncidentDraft,
    loadEntityDraft,
  ]);

  // Lazy-load a node's draft on first selection.
  const ensureDraftLoaded = useCallback(
    async (sel: WikiSelection) => {
      const k = nodeKey(sel);
      if (drafts.has(k)) return;
      setLoadingNode(true);
      try {
        if (sel.kind === "incident") {
          const { draft } = await loadIncidentDraft(sel.id);
          setDrafts((prev) => {
            // Re-check inside the setter so a parallel load doesn't
            // clobber a draft the user already started editing.
            if (prev.has(k)) return prev;
            const next = new Map(prev);
            next.set(k, draft);
            return next;
          });
          setBodyAvailableKeys((prev) => new Set(prev).add(k));
        } else {
          const { draft } = await loadEntityDraft(sel.type, sel.name);
          setDrafts((prev) => {
            if (prev.has(k)) return prev;
            const next = new Map(prev);
            next.set(k, draft);
            return next;
          });
          setBodyAvailableKeys((prev) => new Set(prev).add(k));
        }
      } catch (e) {
        // 404s mean the entity reference is in the graph but no file
        // exists yet — leave bodyAvailable false so the body tab
        // disables itself. Other errors surface in actionError.
        if (e instanceof ApiError && e.status === 404) {
          // No-op; bodyAvailableKeys does not gain k.
        } else {
          setActionError(e instanceof ApiError ? e.message : String(e));
        }
      } finally {
        setLoadingNode(false);
      }
    },
    [drafts, loadIncidentDraft, loadEntityDraft],
  );

  // ── Selection handlers ────────────────────────────────────────────────────

  const handleGraphSelect = useCallback(
    (sel: WikiGraphSelection) => {
      setSelection(sel);
      void ensureDraftLoaded(sel);
    },
    [ensureDraftLoaded],
  );

  // beforeunload — warn if anything is dirty across the session.
  const dirtyCount = useMemo(
    () => Array.from(drafts.values()).filter(draftDirty).length,
    [drafts],
  );
  const dirtyKeys = useMemo(() => {
    const s = new Set<string>();
    for (const [k, d] of drafts) {
      if (draftDirty(d)) s.add(k);
    }
    return s;
  }, [drafts]);

  useEffect(() => {
    function handler(e: BeforeUnloadEvent) {
      if (dirtyCount > 0) {
        e.preventDefault();
        e.returnValue = "";
      }
    }
    window.addEventListener("beforeunload", handler);
    return () => window.removeEventListener("beforeunload", handler);
  }, [dirtyCount]);

  // Listen for wiki-approved events from WikiProposalCard so the editor
  // refetches the freshly-committed file in place — covers both the
  // "+ new wiki entry" flow (we seeded an empty draft on a 404) and the
  // "iterate on existing wiki" flow (the agent rewrote the root via
  // approve). We skip if the matching draft is dirty so we don't
  // clobber operator edits in flight.
  useEffect(() => {
    if (props.rootKind !== "incident") return;
    const rootId = props.rootId;
    function onApproved(e: Event) {
      const detail = (e as CustomEvent).detail as
        | { slug?: string; stubsCreated?: string[] }
        | undefined;
      if (!detail || detail.slug !== rootId) return;
      const k = `incident:${rootId}`;
      const cur = drafts.get(k);
      if (cur && draftDirty(cur)) return;
      void (async () => {
        try {
          const { draft } = await loadIncidentDraft(rootId);
          setDrafts((prev) => new Map(prev).set(k, draft));
          setBodyAvailableKeys((prev) => new Set(prev).add(k));
          setRootError(null);
          // Approved proposal has landed on disk — clear the pending
          // state and bounce out of the proposal tab so the operator
          // sees the saved draft in the body editor.
          if (pendingProposal && pendingProposal.slug === rootId) {
            setDismissedProposalIDs((prev) => {
              const next = new Set(prev);
              next.add(pendingProposal.proposal_id);
              return next;
            });
            setPendingProposal(null);
          }
          setTab((cur) => (cur === "proposal" ? "body" : cur));
        } catch {
          /* swallow — leaving prior state is safer than wiping it */
        }
      })();
    }
    window.addEventListener("c1:wiki-approved", onApproved);
    return () => window.removeEventListener("c1:wiki-approved", onApproved);
  }, [
    props.rootKind,
    (props as { rootId?: string }).rootId,
    drafts,
    loadIncidentDraft,
    pendingProposal,
  ]);

  // Deep-link entry: when the URL carries ?proposal=<id>, hydrate the
  // pendingProposal from the server's draft on mount so the sidenav
  // shortcut works without the chat drawer ever opening. Runs once;
  // subsequent in-app navigation owns the proposal state via the chat
  // drawer's handler.
  const initialProposalLoadedRef = useRef(false);
  useEffect(() => {
    if (!initialProposalID || initialProposalLoadedRef.current) return;
    initialProposalLoadedRef.current = true;
    let cancelled = false;
    (async () => {
      try {
        const res = await api.getWikiProposal(initialProposalID);
        if (cancelled) return;
        // Only hydrate while the proposal is still pending. Resolved
        // proposals fall back to nothing — the operator can see the
        // outcome in the chat transcript if they need it.
        if (res.status !== "pending" || !res.new_md || !res.slug) return;
        setPendingProposal({
          kind: "wiki_proposal_draft",
          proposal_id: res.proposal_id,
          slug: res.slug,
          new_md: res.new_md,
          base_md: res.base_md,
          is_new: res.is_new ?? false,
          new_entities: res.new_entities,
        });
        if (initialTabHint === "proposal") setTab("proposal");
      } catch {
        /* swallow — leaving the editor in its default state is fine */
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [initialProposalID, initialTabHint]);

  // ── Edit dispatchers ──────────────────────────────────────────────────────

  const patchDraft = useCallback(
    (k: string, patch: Partial<IncidentDraft> | Partial<EntityDraft>) => {
      setDrafts((prev) => {
        const cur = prev.get(k);
        if (!cur) return prev;
        const next = new Map(prev);
        next.set(k, { ...cur, ...patch } as Draft);
        return next;
      });
    },
    [],
  );

  // ── Save ──────────────────────────────────────────────────────────────────

  const saveAll = useCallback(async () => {
    if (saving) return;
    const dirty = Array.from(drafts.entries()).filter(([, d]) => draftDirty(d));
    if (dirty.length === 0) return;
    // Sort: incidents first, then entities, then by key for stability.
    dirty.sort(([ka], [kb]) => {
      const aInc = ka.startsWith("incident:");
      const bInc = kb.startsWith("incident:");
      if (aInc !== bInc) return aInc ? -1 : 1;
      return ka.localeCompare(kb);
    });
    setSaving(true);
    setSaveErrors({});
    const newErrors: Record<string, string[]> = {};
    const toRefresh: string[] = [];
    for (const [k, d] of dirty) {
      try {
        if (d.kind === "incident") {
          const res = await wikiApi.updateEntry(d.id, {
            title: d.title,
            severity: d.severity || "",
            markdown: d.body,
          });
          if (!res.ok) {
            newErrors[k] = res.errors ?? ["save failed"];
            continue;
          }
        } else {
          const res = await wikiApi.updateEntity(d.type, d.name, {
            markdown: d.body,
          });
          if (!res.ok) {
            newErrors[k] = res.errors ?? ["save failed"];
            continue;
          }
        }
        toRefresh.push(k);
      } catch (e) {
        newErrors[k] = [e instanceof ApiError ? e.message : String(e)];
      }
    }
    // For successful saves: refresh to pick up the post-commit
    // unsynced flag and reset original snapshots.
    for (const k of toRefresh) {
      const d = drafts.get(k);
      if (!d) continue;
      try {
        if (d.kind === "incident") {
          const { draft } = await loadIncidentDraft(d.id);
          setDrafts((prev) => {
            const next = new Map(prev);
            next.set(k, draft);
            return next;
          });
        } else {
          const { draft } = await loadEntityDraft(d.type, d.name);
          setDrafts((prev) => {
            const next = new Map(prev);
            next.set(k, draft);
            return next;
          });
        }
      } catch {
        /* refresh failure is non-fatal; the operator can retry */
      }
    }
    setSaveErrors(newErrors);
    setSaving(false);
  }, [drafts, saving, loadIncidentDraft, loadEntityDraft]);

  // ── Discard ───────────────────────────────────────────────────────────────

  const discardAll = useCallback(async () => {
    if (dirtyCount === 0) return;
    const ok = await dialog.confirm({
      title: `Discard unsaved edits across ${dirtyCount} node${dirtyCount !== 1 ? "s" : ""}?`,
      body: "Reverts every dirty draft to its on-disk content. This cannot be undone.",
      confirmLabel: "Discard",
      danger: true,
    });
    if (!ok) return;
    setDrafts((prev) => {
      const next = new Map(prev);
      for (const [k, d] of prev) {
        if (!draftDirty(d)) continue;
        if (d.kind === "incident") {
          next.set(k, {
            ...d,
            title: d.original.title,
            severity: d.original.severity,
            body: d.original.body,
          });
        } else {
          next.set(k, { ...d, body: d.original.body });
        }
      }
      return next;
    });
    setSaveErrors({});
  }, [dialog, dirtyCount]);

  // ── Resolve / delete (only for incident root) ─────────────────────────────

  const isIncidentRoot = props.rootKind === "incident";
  const rootDraft = drafts.get(nodeKey(initialSelection));
  const rootIncident =
    rootDraft && rootDraft.kind === "incident" ? rootDraft : null;
  const isResolved = rootIncident?.status === "resolved";
  const wikiReady = capabilities?.wiki.valid ?? false;

  async function handleResolve() {
    if (!isIncidentRoot || resolving || isResolved || !rootIncident) return;
    setResolving(true);
    setActionError(null);
    try {
      await api.resolveWikiEntry(rootIncident.id);
      // Refresh the root draft.
      const { draft } = await loadIncidentDraft(rootIncident.id);
      setDrafts((prev) => {
        const next = new Map(prev);
        next.set(`incident:${rootIncident.id}`, draft);
        return next;
      });
    } catch (e) {
      setActionError(e instanceof Error ? e.message : String(e));
    } finally {
      setResolving(false);
    }
  }

  // ── Push-PR bundle ────────────────────────────────────────────────────────

  // Bundle = every draft that isn't already synced. Saved files flip
  // to local-drift after their commit; the refresh-after-save above
  // pulls the new syncState in. Skipping ONLY the explicit "synced"
  // case (rather than allow-listing "local-drift") keeps the bundle
  // robust if the wiki classifier ever grows richer states like
  // "local-only" — those drafts also need pushing and would be
  // silently dropped by an allow-list filter.
  const bundleFiles = useMemo<WikiBundleFile[]>(() => {
    const files: WikiBundleFile[] = [];
    for (const d of drafts.values()) {
      if (d.syncState.status === "synced") continue;
      if (d.kind === "incident") {
        files.push({ kind: "entry", slug: d.id });
      } else {
        files.push({ kind: "entity", entity_type: d.type, entity_name: d.name });
      }
    }
    return files;
  }, [drafts]);

  const wikiRepoConfigured = (capabilities?.wiki.repo ?? "").length > 0;
  const canPushPR =
    (capabilities?.gh.authenticated ?? false) &&
    (capabilities?.wiki.valid ?? false) &&
    wikiRepoConfigured &&
    bundleFiles.length > 0 &&
    dirtyCount === 0;

  const pushDisabledReason = !capabilities
    ? "loading capabilities…"
    : !capabilities.gh.authenticated
      ? `gh not ready: ${capabilities.gh.reason ?? "gh CLI not authenticated"}`
      : !capabilities.wiki.valid
        ? `wiki not ready: ${capabilities.wiki.reason ?? "configure wiki path"}`
        : !wikiRepoConfigured
          ? "no upstream wiki repo configured — set defaults.wiki_repo in your profile and restart the launcher"
          : dirtyCount > 0
            ? "save your edits first"
            : bundleFiles.length === 0
              ? "no locally-committed-but-unpushed files to push"
              : "";

  // Stable handler for the drawer's onPendingProposalsChange. Using
  // useCallback with [] avoids a render loop: an inline arrow would have a
  // fresh identity each render, causing the drawer's lift useEffect to re-fire
  // and call setPendingProposal unconditionally (the Map-shaped new-feature
  // state has no Object.is bail-out like the old null/primitive did).
  // dismissedProposalIDs is read via ref so the closure stays stable.
  const handleDrawerProposals = useCallback(
    (payloads: (ProposalDraftPayload | WikiProposalPayload)[]) => {
      // Wiki sessions have ≤1 pending proposal in scope. The
      // parser emits only wiki_proposal_draft kinds, so pick the
      // latest non-dismissed entry. Reverse iteration grabs the
      // most recent.
      const wikiPayloads = payloads as WikiProposalPayload[];
      const wiki = [...wikiPayloads].reverse().find(
        (p) => !dismissedProposalIDsRef.current.has(p.proposal_id),
      );
      setPendingProposal(wiki ?? null);
    },
    [],
  );

  // ── Render ────────────────────────────────────────────────────────────────

  if (loadingRoot) {
    return (
      <div className="flex items-center gap-2 px-6 py-12 text-sm text-zinc-400">
        <Spinner /> loading…
      </div>
    );
  }
  if (rootError) {
    return (
      <div className="mx-auto max-w-3xl px-6 py-12">
        <div className="rounded-lg border border-red-900/60 bg-red-950/40 p-4 text-sm text-red-200/90">
          {rootError}
        </div>
      </div>
    );
  }

  const selectedKey = nodeKey(selection);
  const selectedDraft = drafts.get(selectedKey) ?? null;
  const selectedDirty = selectedDraft ? draftDirty(selectedDraft) : false;
  const view = viewFromDraft(selectedDraft);
  const bodyAvailable = bodyAvailableKeys.has(selectedKey);

  // Tab disabled state when the current selection has no body file on
  // disk yet (ensureDraftLoaded got a 404 — typically a graph entity
  // reference that hasn't been written).
  const bodyTabDisabled = !bodyAvailable;
  const bodyLabel = selection.kind === "incident" ? "post-mortem" : "notes";
  const bodyIdentity =
    selection.kind === "incident"
      ? selection.id
      : `${selection.type}/${selection.name}`;

  // Header breadcrumb + title.
  const rootTitle =
    rootIncident?.title ||
    (props.rootKind === "incident"
      ? props.rootId
      : `${props.rootType}/${props.rootName}`);
  const rootSubtitle =
    props.rootKind === "incident" ? "incidents" : "entities";

  return (
    <div data-testid="triagent-wiki-editor" className="flex h-full flex-col px-6 py-6">
      {/* Breadcrumb */}
      <nav className="mb-3 flex items-center gap-3 text-xs text-zinc-500">
        <Link
          href="/wiki"
          className="inline-flex items-center gap-1.5 text-sm text-zinc-400 transition hover:text-zinc-200"
        >
          <ArrowLeftIcon className="h-3.5 w-3.5" />
          back to wiki
        </Link>
        <span className="flex items-center gap-1.5">
          <span>{rootSubtitle}</span>
          <span>/</span>
          <span className="font-mono text-zinc-300">
            {props.rootKind === "incident"
              ? props.rootId
              : `${props.rootType}/${props.rootName}`}
          </span>
        </span>
      </nav>

      {/* Header */}
      <header className="mb-3 flex items-baseline justify-between gap-4">
        <div className="min-w-0 flex-1 space-y-1">
          <h1 className="truncate text-xl font-semibold tracking-tight text-zinc-100">
            {rootTitle}
          </h1>
          {rootIncident && (
            <div className="flex items-baseline gap-2 text-xs">
              {rootIncident.syncState.status !== "synced" && (
                <span title={rootIncident.syncState.reason ?? "local changes not yet pushed upstream"}>
                  <UnsyncedIcon className="h-4 w-4 text-amber-400" />
                </span>
              )}
              {rootIncident.severity && (
                <SeverityBadge severity={rootIncident.severity} />
              )}
              {rootIncident.status && (
                <StatusBadge status={rootIncident.status} />
              )}
              <span className="text-zinc-500">{rootIncident.date}</span>
            </div>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {isIncidentRoot && (
            <button
              type="button"
              onClick={handleResolve}
              disabled={!wikiReady || isResolved || resolving}
              title={
                isResolved
                  ? "Already resolved"
                  : !wikiReady
                    ? "wiki not ready"
                    : "Mark this incident as resolved"
              }
              className={
                wikiReady && !isResolved
                  ? // Disabled fall-through (e.g. mid-resolve) matches
                    // BTN_SECONDARY's muted disabled palette so the
                    // button reads as "not actionable right now"
                    // without the half-bright emerald border that
                    // disabled:opacity-50 used to leave behind.
                    `${BTN_BASE} border-emerald-800 bg-emerald-950/40 text-emerald-300 hover:border-emerald-700 hover:bg-emerald-950 hover:text-emerald-200 disabled:cursor-not-allowed disabled:border-zinc-900 disabled:bg-transparent disabled:text-zinc-600 disabled:hover:bg-transparent disabled:hover:text-zinc-600`
                  : BTN_GATED
              }
            >
              {resolving ? "resolving…" : "resolve"}
            </button>
          )}
          <button
            type="button"
            onClick={discardAll}
            disabled={dirtyCount === 0 || saving}
            title="discard unsaved edits across every dirty draft and reload the saved versions"
            className={BTN_SECONDARY}
          >
            <RevertIcon className="h-3.5 w-3.5" />
            discard edits
          </button>
          <button
            type="button"
            onClick={saveAll}
            disabled={dirtyCount === 0 || saving}
            className={BTN_PRIMARY}
          >
            {saving ? "saving…" : `save${dirtyCount > 0 ? ` (${dirtyCount})` : ""}`}
          </button>
          <button
            type="button"
            onClick={() => setShowPushModal(true)}
            disabled={!canPushPR}
            title={canPushPR ? "Open a PR with all unsynced files" : pushDisabledReason}
            className={canPushPR ? BTN_SECONDARY : BTN_GATED}
          >
            <GitHubIcon className="h-3.5 w-3.5" />
            push as PR
            {bundleFiles.length > 1 && ` (${bundleFiles.length})`}
          </button>
          <button
            type="button"
            onClick={() => {
              setHadChat(true);
              setChatOpen((v) => !v);
            }}
            aria-pressed={chatOpen}
            title={
              chatOpen
                ? "collapse chat (keeps session alive — toggle again to resume)"
                : "open agent chat — refine this wiki entry with the assistant"
            }
            className={chatOpen ? BTN_SECONDARY_ACTIVE : BTN_SECONDARY}
          >
            <ChatBubbleIcon className="h-3.5 w-3.5" />
            chat
          </button>
        </div>
      </header>

      {actionError && (
        <p className="mb-2 text-xs text-red-400">{actionError}</p>
      )}

      {Object.keys(saveErrors).length > 0 && (
        <div className="mb-2 rounded border border-red-900/60 bg-red-950/40 p-2 text-xs text-red-200/90">
          <div className="mb-1 font-medium">Some saves failed:</div>
          <ul className="space-y-0.5">
            {Object.entries(saveErrors).map(([k, errs]) => (
              <li key={k}>
                <span className="font-mono">{k}</span>
                {errs.map((e) => (
                  <span key={e}> — {e}</span>
                ))}
              </li>
            ))}
          </ul>
        </div>
      )}

      {/* Main body */}
      <div className="flex min-h-0 flex-1 gap-3">
        <div className="flex flex-1 flex-col gap-2 min-w-0">
          <div className="flex items-center gap-1 border-b border-zinc-800 pl-1">
            <Tab
              active={tab === "diagram"}
              onClick={() => setTab("diagram")}
              label="diagram"
            />
            <Tab
              active={tab === "body"}
              onClick={() => {
                if (bodyTabDisabled) return;
                setTab("body");
              }}
              label={bodyLabel}
              disabled={bodyTabDisabled}
            />
            <Tab
              active={tab === "proposal"}
              onClick={() => {
                if (!pendingProposal) return;
                setTab("proposal");
              }}
              label="AI proposal"
              disabled={!pendingProposal}
              badge={
                pendingProposal ? (
                  <span
                    aria-hidden
                    className="inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-sky-400"
                  />
                ) : null
              }
              trailing={
                pendingProposal ? (
                  <button
                    type="button"
                    onClick={async (e) => {
                      e.stopPropagation();
                      const id = pendingProposal.proposal_id;
                      try {
                        await api.declineWikiProposal(id);
                      } catch {
                        /* best-effort — server may have already
                           expired or the chat session terminated */
                      }
                      window.dispatchEvent(
                        new CustomEvent("c1:wiki-proposals-changed"),
                      );
                      setDismissedProposalIDs((prev) => {
                        const next = new Set(prev);
                        next.add(id);
                        return next;
                      });
                      setPendingProposal(null);
                      if (tab === "proposal") {
                        setTab(bodyTabDisabled ? "diagram" : "body");
                      }
                    }}
                    title="discard proposal"
                    aria-label="discard proposal"
                    className="-mr-1 ml-1 inline-flex h-5 w-5 items-center justify-center rounded-full text-zinc-500 transition hover:bg-rose-500/15 hover:text-rose-300"
                  >
                    <svg
                      viewBox="0 0 16 16"
                      fill="none"
                      stroke="currentColor"
                      strokeWidth="2"
                      strokeLinecap="round"
                      className="h-3 w-3"
                      aria-hidden
                    >
                      <path d="M4 4l8 8M12 4l-8 8" />
                    </svg>
                  </button>
                ) : null
              }
            />
          </div>
          <div className="min-h-0 flex-1">
            {tab === "diagram" && (
              <div className="h-full">
                <WikiGraph
                  {...(props.rootKind === "incident"
                    ? { rootKind: "incident" as const, rootId: props.rootId }
                    : {
                        rootKind: "entity" as const,
                        rootType: props.rootType,
                        rootName: props.rootName,
                      })}
                  onSelect={handleGraphSelect}
                  selectedKey={selectedKey}
                  dirtyKeys={dirtyKeys}
                />
              </div>
            )}
            {tab === "body" &&
              (selectedDraft ? (
                <WikiBodyTab
                  label={bodyLabel}
                  identity={bodyIdentity}
                  body={selectedDraft.body}
                  onChange={(next) =>
                    patchDraft(selectedKey, { body: next } as Partial<Draft>)
                  }
                />
              ) : (
                <div className="p-3 text-sm text-zinc-500">loading body…</div>
              ))}
            {tab === "proposal" && pendingProposal && (
              <div className="h-full min-h-0 overflow-y-auto p-3">
                <WikiProposalCard
                  payload={pendingProposal}
                  capabilities={capabilities ?? FALLBACK_CAPABILITIES}
                  onSendRefinement={async () => {
                    /* refinement notes go through the chat composer
                       directly; the tab's WikiProposalCard doesn't pipe
                       them through to the agent. */
                  }}
                />
              </div>
            )}
          </div>
        </div>

        <aside className="flex w-96 shrink-0 flex-col rounded border border-zinc-800 bg-zinc-900/30">
          <div className="min-h-0 flex-1 space-y-3 overflow-y-auto p-3">
            {loadingNode && !selectedDraft ? (
              <div className="flex items-center gap-2 text-sm text-zinc-500">
                <Spinner /> loading node…
              </div>
            ) : (
              <WikiNodeEditor
                view={view}
                dirty={selectedDirty}
                bodyAvailable={bodyAvailable}
                onOpenBody={() => {
                  if (bodyAvailable) setTab("body");
                }}
                onIncidentChange={(patch) =>
                  patchDraft(selectedKey, patch as Partial<Draft>)
                }
              />
            )}
          </div>
        </aside>
      </div>

      {chatOpen && (
        <EditorChatDrawer
          open={chatOpen}
          subject={
            props.rootKind === "incident"
              ? { kind: "wiki", wiki: { kind: "entry", id: props.rootId } }
              : {
                  kind: "wiki",
                  wiki: {
                    kind: "entity",
                    id: `${props.rootType}/${props.rootName}`,
                  },
                }
          }
          proposalParser={parseWikiProposal}
          proposalToolName={PROPOSE_WIKI_DRAFT_TOOL_NAME}
          onCollapse={() => setChatOpen(false)}
          onTerminate={() => setChatOpen(false)}
          onPendingProposalsChange={handleDrawerProposals}
          dismissedProposalIDs={dismissedProposalIDs}
          emptyHint={
            props.rootKind === "incident" ? (
              <>
                Ask the assistant to draft or refine this incident wiki entry
                — e.g. <em>"summarise the slack thread into the incident
                section"</em> or <em>"link the affected services as
                entities"</em>. It drafts the markdown, validates the
                frontmatter, and proposes a diff for you to approve.
              </>
            ) : (
              <>
                Ask the assistant to draft or refine this entity's wiki page
                — e.g. <em>"pull in the canonical description from the
                incidents that link here"</em> or <em>"tighten the
                summary"</em>. It drafts the markdown and proposes a diff
                for you to approve.
              </>
            )
          }
        />
      )}
      {!chatOpen && hadChat && (
        <button
          type="button"
          onClick={() => setChatOpen(true)}
          title="resume chat session"
          aria-label="resume chat"
          className="fixed bottom-6 right-6 z-30 inline-flex h-12 w-12 items-center justify-center rounded-full bg-sky-600 text-white shadow-lg transition hover:bg-sky-500 hover:shadow-xl"
        >
          <ChatBubbleIcon className="h-5 w-5" />
          <span
            aria-hidden
            className="absolute right-1 top-1 h-2 w-2 animate-pulse rounded-full bg-emerald-400 ring-2 ring-zinc-950"
          />
        </button>
      )}

      {showPushModal && capabilities && (
        <PushWikiPRModal
          bundle={{
            rootDisplay:
              props.rootKind === "incident"
                ? props.rootId
                : `${props.rootType}/${props.rootName}`,
            files: bundleFiles,
          }}
          capabilities={capabilities}
          onClose={() => setShowPushModal(false)}
          onSuccess={() => {
            // Once the PR is open, every bundled file is no longer
            // "unsynced" relative to the new branch. Re-fetch each
            // draft so the unsynced flag clears and the bundle empties.
            // The modal stays open so the success view can show the
            // PR link; the operator closes it themselves.
            void Promise.all(
              bundleFiles.map(async (f) => {
                const k =
                  f.kind === "entry"
                    ? `incident:${f.slug}`
                    : `entity:${f.entity_type}:${f.entity_name}`;
                try {
                  if (f.kind === "entry") {
                    const { draft } = await loadIncidentDraft(f.slug);
                    setDrafts((prev) => {
                      const next = new Map(prev);
                      next.set(k, draft);
                      return next;
                    });
                  } else {
                    const { draft } = await loadEntityDraft(
                      f.entity_type,
                      f.entity_name,
                    );
                    setDrafts((prev) => {
                      const next = new Map(prev);
                      next.set(k, draft);
                      return next;
                    });
                  }
                } catch {
                  /* non-fatal */
                }
              }),
            );
          }}
        />
      )}
    </div>
  );
}

// ── Helpers ──────────────────────────────────────────────────────────────────

function viewFromDraft(
  d: Draft | null,
): IncidentDraftView | EntityDraftView | null {
  if (!d) return null;
  if (d.kind === "incident") {
    return {
      kind: "incident",
      id: d.id,
      title: d.title,
      severity: d.severity,
      status: d.status,
      date: d.date,
      services: d.services,
      errors: d.errors,
      symptoms: d.symptoms,
      body: d.body,
    };
  }
  return {
    kind: "entity",
    type: d.type,
    name: d.name,
    created: d.created,
    body: d.body,
    backlinks: d.backlinks,
  };
}

function Tab({
  active,
  onClick,
  label,
  disabled,
  badge,
  trailing,
}: {
  active: boolean;
  onClick: () => void;
  label: string;
  disabled?: boolean;
  badge?: React.ReactNode;
  trailing?: React.ReactNode;
}) {
  return (
    <div
      role="tab"
      aria-selected={active}
      aria-disabled={disabled || undefined}
      onClick={onClick}
      className={
        "inline-flex items-center gap-2 rounded-t border-b-2 px-3 py-1.5 text-sm font-medium transition " +
        (disabled
          ? "cursor-not-allowed border-transparent text-zinc-700"
          : active
            ? "border-sky-500 bg-zinc-900/40 text-zinc-100 cursor-pointer"
            : "cursor-pointer border-transparent text-zinc-500 hover:text-zinc-200")
      }
    >
      {badge}
      {label}
      {trailing}
    </div>
  );
}

function SeverityBadge({ severity }: { severity: string }) {
  const cls =
    severity === "sev1"
      ? "bg-red-900/60 text-red-300 border-red-800"
      : severity === "sev2"
        ? "bg-amber-900/50 text-amber-300 border-amber-800"
        : "bg-sky-900/50 text-sky-300 border-sky-800";
  return (
    <span className={`rounded border px-1.5 py-0.5 text-xs font-medium ${cls}`}>
      {severity}
    </span>
  );
}

function StatusBadge({ status }: { status: string }) {
  const cls =
    status === "resolved"
      ? "bg-emerald-900/50 text-emerald-300 border-emerald-800"
      : status === "open"
        ? "bg-zinc-800 text-zinc-300 border-zinc-700"
        : "bg-zinc-800/60 text-zinc-500 border-zinc-700";
  return (
    <span className={`rounded border px-1.5 py-0.5 text-xs ${cls}`}>
      {status}
    </span>
  );
}

