"use client";

import {
  useCallback,
  useEffect,
  useMemo,
  useReducer,
  useRef,
  useState,
} from "react";
import { useSearchParams } from "next/navigation";
import { api, ApiError, type Capabilities, type SyncState, type ToolEntry } from "@/lib/api";
import { useDialog } from "@/lib/dialog";
import { EditorChatDrawer } from "./EditorChatDrawer";
import { ProposalCard, type ProposalDraftPayload } from "./ProposalCard";
import { type WikiProposalPayload } from "./WikiProposalCard";
import { PushPRModal } from "./PushPRModal";
import {
  dumpPlaybookYAML,
  emptyPlaybook,
  LOCAL_DRAFT_PREFIX,
  parsePlaybookYAML,
  type Playbook,
  type PlaybookCommit,
  type PlaybookListItem,
  type PlaybookNode,
  validate,
} from "@/lib/playbook";
import { CommitsDropdown } from "./CommitsDropdown";
import { EntityChipsInput } from "./EntityChipsInput";
import { PLAYBOOK_DRAFT_TAGS_CHANGED, type PlaybookDraftTagsDetail } from "./PlaybookCorrelationPanel";
import { SaveDialog } from "./SaveDialog";
import { NodeEditor } from "./NodeEditor";
import { PlaybookGraph } from "./PlaybookGraph";
import { Spinner } from "./Spinner";
import { YamlPanel } from "./YamlPanel";
import {
  ArrowLeftIcon,
  ChatBubbleIcon,
  GitHubIcon,
  RevertIcon,
  TrashIcon,
} from "./Icons";
import {
  BTN_DANGER,
  BTN_GATED,
  BTN_PRIMARY,
  BTN_SECONDARY,
  BTN_SECONDARY_ACTIVE,
} from "@/lib/buttons";
import { pushRecent as recordRecentPlaybook } from "@/lib/recent-playbooks";

// Parser for the playbook drawer. Validates the three required fields
// the editor's UI relies on, dedupes tabs by playbook_id (latest draft
// for each id wins).
function parsePlaybookProposal(
  raw: unknown,
): { key: string; payload: ProposalDraftPayload } | null {
  const r = raw as Partial<ProposalDraftPayload>;
  if (
    typeof r?.proposal_id !== "string" ||
    typeof r?.playbook_id !== "string" ||
    typeof r?.new_yaml !== "string"
  ) {
    return null;
  }
  return { key: r.playbook_id, payload: r as ProposalDraftPayload };
}

// isStubbedDraft returns true when the draft has not been substantively
// edited from the empty template — i.e., the operator typed at most an
// id (which we strip from the comparison). The `active` field is also
// ignored because it's stamped at write time, not authored by the
// operator. Used by the __new approval flow to decide whether to
// navigate to a freshly-saved sibling proposal: if the draft is still
// the stub, navigation isn't disruptive (no operator work to lose).
function isStubbedDraft(draft: Playbook, original: Playbook): boolean {
  const stripIDActive = (p: Playbook) => {
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const { id, active, ...rest } = p;
    return rest;
  };
  return JSON.stringify(stripIDActive(draft)) === JSON.stringify(stripIDActive(original));
}

type Props = {
  // The id from the URL. "__new" creates a fresh template.
  id: string;
  onBack: () => void;
  // Bumped by the parent after a successful Save/Delete so the sidebar
  // list refetches.
  onMutated: () => void;
  // Navigate to another playbook in the editor (for handoff links).
  // Same surface PlaybookList uses; bubbled up so a terminal node's
  // handoff entry can take the operator straight to the target.
  onOpenPlaybook: (id: string) => void;
};

type LoadState =
  | { kind: "loading" }
  | {
      kind: "loaded";
      original: Playbook;
      source: SourceTag;
      // True when the playbook ships bundled with the launcher
      // (system tier). Disables save / delete / type-change /
      // push-PR — locked entries are owned by the launcher binary,
      // not the operator.
      locked: boolean;
      isNew: boolean;
      disabled: boolean;
      // Commit history for the commits dropdown (up to 50, merged
      // across upstream + local repos). Populated from the detail
      // response; empty for brand-new (unsaved) playbooks.
      commits: PlaybookCommit[];
      // The sha the operator is currently viewing. Undefined means
      // HEAD (latest). Resets to undefined after a save.
      viewedCommit?: string;
      // Raw upstream-clone YAML when this id has an upstream
      // counterpart. Empty for user-only playbooks. The push-PR gate
      // compares the operator's draft against this — a draft that
      // matches upstream produces a no-op PR, so the button locks.
      upstreamYAML?: string;
    }
  | { kind: "error"; message: string };

type SourceTag = "plugin" | "system" | "user" | "override" | "broken";

// Reducer-style updates against the editable Playbook. Each field-level
// edit dispatches a partial that's merged in. Node operations (rename,
// delete, add) take dedicated actions because they touch multiple fields.
type Action =
  | { type: "set"; next: Playbook }
  | {
      type: "setMeta";
      meta: Partial<
        Pick<
          Playbook,
          | "id"
          | "symptom"
          | "description"
          | "entrypoint"
          | "type"
          | "active"
          | "services"
          | "errors"
          | "symptoms"
        >
      >;
    }
  | { type: "setNode"; nodeId: string; node: PlaybookNode }
  | { type: "renameNode"; oldId: string; newId: string }
  | { type: "addNode"; nodeId: string }
  | { type: "deleteNode"; nodeId: string };

// pickActiveCommitSha returns the sha of the commit that wrote the
// currently-active YAML for a playbook, derived from the merged
// commits list + the source slot. Newest-first ordering means the
// first source-matching commit is the active one. Returns undefined
// when there's no candidate (e.g. a system playbook that lives only
// in the launcher binary, or a fresh playbook with no history).
function pickActiveCommitSha(
  commits: PlaybookCommit[],
  source: PlaybookListItem["source"],
): string | undefined {
  // user / override: latest commit on the local user-playbooks repo
  // is what's on disk and active.
  // plugin: latest commit on the upstream-playbooks repo wins.
  // system / broken / disabled: no commit history we can pin to.
  let want: PlaybookCommit["source"] | undefined;
  if (source === "user" || source === "override") want = "local";
  else if (source === "plugin") want = "upstream";
  if (!want) return undefined;
  const m = commits.find((c) => c.source === want);
  return m?.sha;
}

type ViewMode =
  | { kind: "graph" }
  | { kind: "yaml" }
  | { kind: "proposal"; proposalId: string };

function reduce(state: Playbook, action: Action): Playbook {
  switch (action.type) {
    case "set":
      return action.next;
    case "setMeta":
      return { ...state, ...action.meta };
    case "setNode":
      return {
        ...state,
        nodes: { ...state.nodes, [action.nodeId]: action.node },
      };
    case "renameNode": {
      if (action.oldId === action.newId || !action.newId) return state;
      if (state.nodes[action.newId]) return state; // collision; ignored
      const nextNodes: Record<string, PlaybookNode> = {};
      for (const [id, node] of Object.entries(state.nodes)) {
        nextNodes[id === action.oldId ? action.newId : id] = {
          ...node,
          next: node.next?.map((b) =>
            b.goto === action.oldId ? { ...b, goto: action.newId } : b,
          ),
        };
      }
      return {
        ...state,
        nodes: nextNodes,
        entrypoint:
          state.entrypoint === action.oldId ? action.newId : state.entrypoint,
      };
    }
    case "addNode": {
      if (state.nodes[action.nodeId]) return state;
      return {
        ...state,
        nodes: {
          ...state.nodes,
          [action.nodeId]: { description: "" },
        },
      };
    }
    case "deleteNode": {
      const nextNodes: Record<string, PlaybookNode> = { ...state.nodes };
      delete nextNodes[action.nodeId];
      return { ...state, nodes: nextNodes };
    }
  }
}

export function PlaybookEditor({ id, onBack, onMutated, onOpenPlaybook }: Props) {
  const [load, setLoad] = useState<LoadState>({ kind: "loading" });
  // Bumped after a successful save so the load effect re-fetches the
  // playbook + its commits. Without this the editor sits on the
  // pre-save commits[] / source and the version dropdown reads
  // "latest" instead of the freshly-created commit.
  const [refetchKey, setRefetchKey] = useState(0);
  const [tools, setTools] = useState<ToolEntry[]>([]);
  const [capabilities, setCapabilities] = useState<Capabilities | null>(null);
  const [selectedNode, setSelectedNode] = useState<string | null>(null);
  // Right-aside view: "node" is the per-node editor; "tags" surfaces
  // the playbook-level entity tag inputs. Local state — defaults to
  // "node" because that's the per-iteration editing surface; tags are
  // playbook-level metadata you set once and revisit rarely.
  const [asideTab, setAsideTab] = useState<"node" | "tags">("node");
  // All playbook ids — feeds the handoff dropdown in the node editor so
  // operators pick a real target instead of typing free-text.
  const [allPlaybookIds, setAllPlaybookIds] = useState<string[]>([]);
  const [availableTypes, setAvailableTypes] = useState<
    { name: string; description: string }[]
  >([]);
  // Editor view mode — diagram graph, YAML preview, or AI proposal
  // preview as tabs in the editor's main column. Stacking both at
  // once made the workspace unreadable now that the chat drawer covers
  // the bottom of the viewport; the tab triple gives the active view
  // the full vertical space it needs.
  //
  // ViewMode is a discriminated union declared at module scope; proposal
  // mode carries the proposal_id of the selected tab. Switching between
  // proposal tabs only changes the proposalId; switching to graph/yaml
  // drops it.
  const [viewMode, setViewMode] = useState<ViewMode>({ kind: "graph" });
  const [saving, setSaving] = useState(false);
  const [commitMessage, setCommitMessage] = useState("");
  const [showSaveDialog, setShowSaveDialog] = useState(false);
  // Server-authoritative drift state. Driven by the same resolver the
  // list endpoint uses, so the editor's push-PR gate can't disagree
  // with the list's "unsynced" badge. Null = haven't asked yet
  // (initial render before the load + first preview land); we treat
  // null as "permissive" so the push button isn't spuriously blocked.
  const [syncState, setSyncState] = useState<SyncState | null>(null);
  // Pending AI proposals for this editor session, keyed by playbook_id.
  // Latest-replaces semantics: when the agent re-drafts the same id,
  // the tab updates in place. Insertion order = first-seen-each-id
  // order = tab order, which matches the agent's drafting sequence
  // (the prompt asks for dependency-ordered submission, so leaves come
  // first).
  const [pendingProposals, setPendingProposals] = useState<
    Map<string, ProposalDraftPayload>
  >(() => new Map());
  // Ref kept in sync after every commit so onPendingProposalsChange can
  // read the most-recent Map without capturing stale closure state.
  const pendingProposalsRef = useRef<Map<string, ProposalDraftPayload>>(
    new Map(),
  );
  useEffect(() => {
    pendingProposalsRef.current = pendingProposals;
  }, [pendingProposals]);
  const [dismissedProposalIDs, setDismissedProposalIDs] = useState<Set<string>>(
    () => new Set(),
  );
  // Tab strip data: filters dismissed entries so the Map can silently
  // carry them without exposing them in the tab strip. Insertion order
  // is preserved by Map.values().
  const proposalArray = useMemo(
    () =>
      Array.from(pendingProposals.values()).filter(
        (p) => !dismissedProposalIDs.has(p.proposal_id),
      ),
    [pendingProposals, dismissedProposalIDs],
  );
  const [serverErrors, setServerErrors] = useState<string[]>([]);
  const [draft, dispatch] = useReducer(reduce, emptyPlaybook());

  // Broadcast the current draft's tag set whenever the operator edits a
  // chip in the entity-tag inputs. The sidebar's PlaybookCorrelationPanel
  // listens for this event and refetches /related and /related-wiki with
  // the override tags as query params — so the panel previews the
  // correlations the saved playbook *would* have without requiring a
  // save first. Skipped while the editor is still loading (no real id)
  // or for the __new template (no canonical id to scope the event to).
  useEffect(() => {
    if (!draft.id || draft.id.startsWith("__")) return;
    const detail: PlaybookDraftTagsDetail = {
      playbookID: draft.id,
      services: draft.services ?? [],
      errors: draft.errors ?? [],
      symptoms: draft.symptoms ?? [],
    };
    window.dispatchEvent(
      new CustomEvent(PLAYBOOK_DRAFT_TAGS_CHANGED, { detail }),
    );
  }, [draft.id, draft.services, draft.errors, draft.symptoms]);
  const [showPRModal, setShowPRModal] = useState(false);
  // Drawer chat-with-assistant state. chatVersion is captured at the
  // moment the drawer opens so the chat is frozen to the snapshot the
  // operator was viewing — switching version in the editor dropdown
  // afterwards doesn't silently kill the chat. Closing + reopening
  // resnapshots against the now-active version. chatPlaybookId is the
  // matching id snapshot — required so an in-progress chat survives the
  // "+ new playbook" → accepted-proposal navigation: the backend session
  // is keyed by (id, version) at create-time, so locking the subject id
  // to the open-time snapshot keeps SSE pointed at the same session even
  // after draft.id reloads to the freshly-saved playbook.
  const [chatOpen, setChatOpen] = useState(false);
  const [chatVersion, setChatVersion] = useState<string | null>(null);
  const [chatPlaybookId, setChatPlaybookId] = useState<string | null>(null);
  // Snapshot the editor's load.isNew + serialized YAML + type at the
  // moment the operator opens chat. The drawer's subject needs these
  // for the __new flow (so the agent sees the in-progress draft on
  // session creation), but they must NOT come from live editor state
  // — when the URL navigates and the editor briefly enters
  // load.kind === "loading", `load.isNew` and the live YAML aren't
  // accessible. Snapshotting at chat-open lets the drawer render and
  // run its session continuously across the editor's body
  // re-renders.
  const [chatIsNew, setChatIsNew] = useState<boolean>(false);
  const [chatYaml, setChatYaml] = useState<string>("");
  const [chatType, setChatType] = useState<string | undefined>(undefined);
  const [chatSessionId, setChatSessionId] = useState<string | null>(null);
  const dialog = useDialog();

  // Deep-link params: ?proposal=<id>&tab=proposal lets the playbooks
  // sidenav jump straight into the AI proposal tab for a still-pending
  // draft. Captured on mount so subsequent in-app navigation doesn't
  // re-trigger hydration (the editor's chat drawer owns the proposal
  // lifetime after that).
  const searchParams = useSearchParams();
  const initialProposalID = searchParams?.get("proposal") ?? null;
  const initialTabHint = searchParams?.get("tab") ?? null;
  const initialProposalLoadedRef = useRef(false);
  useEffect(() => {
    if (!initialProposalID || initialProposalLoadedRef.current) return;
    initialProposalLoadedRef.current = true;
    let cancelled = false;
    (async () => {
      try {
        const res = await api.getPlaybookProposal(initialProposalID);
        if (cancelled) return;
        // Only hydrate while the proposal is still pending. Resolved
        // proposals fall back to nothing — the operator can see the
        // outcome in the chat transcript if they need it.
        if (
          res.status !== "pending" ||
          !res.new_yaml ||
          !res.playbook_id
        ) {
          return;
        }
        const payload: ProposalDraftPayload = {
          proposal_id: res.proposal_id,
          playbook_id: res.playbook_id,
          base_yaml: res.base_yaml,
          new_yaml: res.new_yaml,
        };
        setPendingProposals((prev) => {
          const next = new Map(prev);
          next.set(payload.playbook_id, payload);
          return next;
        });
        if (initialTabHint === "proposal") {
          setViewMode({ kind: "proposal", proposalId: payload.proposal_id });
        }
      } catch {
        /* swallow — leaving the editor in its default state is fine */
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [initialProposalID, initialTabHint]);

  const discardProposal = useCallback(
    async (p: ProposalDraftPayload) => {
      const ok = await dialog.confirm({
        title: "Discard AI proposal?",
        body: "The pending suggestion is dropped. The chat transcript still shows the diff for reference, but the action buttons disappear.",
        confirmLabel: "Discard",
        cancelLabel: "Keep",
      });
      if (!ok) return;
      try {
        await api.declinePlaybookProposal(p.proposal_id);
      } catch {
        /* best-effort; server may have expired or terminated */
      }
      window.dispatchEvent(new CustomEvent("c1:playbook-proposals-changed"));
      setDismissedProposalIDs((prev) => {
        const next = new Set(prev);
        next.add(p.proposal_id);
        return next;
      });
      setPendingProposals((prev) => {
        const next = new Map(prev);
        next.delete(p.playbook_id);
        return next;
      });
      setViewMode((vm) =>
        vm.kind === "proposal" && vm.proposalId === p.proposal_id
          ? { kind: "graph" }
          : vm,
      );
    },
    [dialog],
  );

  const onProposalResolved = useCallback(
    async (p: ProposalDraftPayload, kind: "approved" | "declined") => {
      setDismissedProposalIDs((prev) => {
        const next = new Set(prev);
        next.add(p.proposal_id);
        return next;
      });
      setPendingProposals((prev) => {
        const next = new Map(prev);
        next.delete(p.playbook_id);
        return next;
      });
      setViewMode((vm) =>
        vm.kind === "proposal" && vm.proposalId === p.proposal_id
          ? { kind: "graph" }
          : vm,
      );
      if (kind !== "approved") return;

      // Sidebar list refresh + Recent-tray surfacing happen even when
      // the editor doesn't navigate (off-subject approvals).
      if (typeof window !== "undefined") {
        window.dispatchEvent(new Event("c1:playbooks-changed"));
      }
      recordRecentPlaybook(p.playbook_id);
      onMutated();

      const fromNew = load.kind === "loaded" && load.isNew;
      const matchesSubject = p.playbook_id === draft.id;

      if (fromNew && load.kind === "loaded" && (matchesSubject || isStubbedDraft(draft, load.original))) {
        // __new mode and the operator hasn't done substantive editing
        // yet — navigate the editor onto the freshly-saved file.
        dialog.notify({
          kind: "success",
          title: `approved ${p.playbook_id}`,
          body: "opening playbook…",
          ttlMs: 4000,
        });
        const targetID = p.playbook_id;
        // Drive the URL via the editor's onOpenPlaybook prop (the page
        // host turns this into router.push("/playbooks?playbook=…")).
        // Direct call is safe here — we're not racing the route
        // transition pipeline anymore now that selection is a search-
        // param replace, not a route push.
        onOpenPlaybook(targetID);

        // Re-anchor the chat session server-side so its stored Subject
        // matches the URL the operator now sees. The drawer's
        // sessionId-gated effects keep the local transcript intact.
        const sid = chatSessionId;
        if (sid) {
          try {
            await api.rebindEditorSession(sid, {
              kind: "playbook",
              playbook: { id: targetID, version: "HEAD" },
            });
            setChatPlaybookId(targetID);
            setChatVersion("HEAD");
            setChatIsNew(false);
            // chatYaml + chatType are only consumed when chatIsNew is
            // true (see drawer mount-site spread). Clear them anyway
            // so a future caller that forgets that gate doesn't read
            // stale __new content.
            setChatYaml("");
            setChatType(undefined);
          } catch (err) {
            dialog.notify({
              kind: "error",
              title: "chat-session retarget failed",
              body: "close + reopen the drawer to resync",
              ttlMs: 6000,
            });
          }
        }
        return;
      }
      if (matchesSubject) {
        setRefetchKey((k) => k + 1);
        return;
      }
      // Non-subject approval — sidebar refreshed; stay on current.
      dialog.notify({
        kind: "success",
        title: `approved ${p.playbook_id}`,
        body: "sidebar refreshed",
      });
    },
    [chatSessionId, dialog, draft, load, onMutated, onOpenPlaybook],
  );

  // Load the playbook from the API (or seed with the empty template for
  // "__new"). Restore an in-progress draft from localStorage if present.
  useEffect(() => {
    let cancelled = false;
    setLoad({ kind: "loading" });

    if (id === "__new") {
      const initial = emptyPlaybook();
      dispatch({ type: "set", next: initial });
      setLoad({
        kind: "loaded",
        original: initial,
        source: "user",
        locked: false,
        isNew: true,
        disabled: false,
        commits: [],
      });
      // Brand-new playbook: nothing on disk to compare against. Seed
      // local-only so the push-PR button reads "your draft will create
      // a real PR" rather than "no diff".
      setSyncState({ status: "local-only", reason: "not yet on upstream" });
      setSelectedNode(initial.entrypoint);
      return () => {
        cancelled = true;
      };
    }

    api
      .getPlaybook(id)
      .then(async (item) => {
        if (cancelled) return;
        let parsed: Playbook;
        try {
          parsed = parsePlaybookYAML(item.yaml);
        } catch (e) {
          setLoad({
            kind: "error",
            message: `Failed to parse YAML: ${e instanceof Error ? e.message : String(e)}`,
          });
          return;
        }
        // Type lives in the directory layout, not the YAML body, so
        // parsePlaybookYAML can't recover it. Seed from the API
        // response (item.type carries the type slot the file is in)
        // so the editor's type dropdown defaults to the current slot
        // and the dirty-tracking comparison stays stable across
        // load → no-op render cycles.
        if (item.type) {
          parsed.type = item.type;
        }
        // localStorage draft restore. Only prompt when the stored draft
        // actually differs from the freshly-fetched original; otherwise
        // it's leftover from a passive view-and-leave session and
        // shouldn't bother the operator.
        const draftRaw = window.localStorage.getItem(LOCAL_DRAFT_PREFIX + id);
        const originalSerialised = JSON.stringify(parsed);
        let restored = false;
        if (draftRaw && draftRaw !== originalSerialised) {
          const ok = await dialog.confirm({
            title: `Unsaved changes for "${id}"`,
            body: "A previous edit session left unsaved changes for this playbook. Restore them?",
            confirmLabel: "Restore",
            cancelLabel: "Discard",
          });
          if (ok) {
            try {
              dispatch({ type: "set", next: JSON.parse(draftRaw) as Playbook });
              restored = true;
            } catch {
              dispatch({ type: "set", next: parsed });
            }
          } else {
            // Operator chose Discard — drop the stale draft so the
            // prompt doesn't reappear on the next visit.
            window.localStorage.removeItem(LOCAL_DRAFT_PREFIX + id);
            dispatch({ type: "set", next: parsed });
          }
        } else {
          dispatch({ type: "set", next: parsed });
        }
        setLoad({
          kind: "loaded",
          original: parsed,
          source: item.source,
          locked: !!item.locked,
          isNew: false,
          disabled: !!item.disabled,
          commits: item.commits ?? [],
          upstreamYAML: item.upstreamYAML,
          // Pre-select the active commit in the dropdown rather than
          // leaving it on the bare "latest" option. The active commit
          // = newest commit whose source matches whichever side
          // (local user repo / upstream plugin repo) currently owns
          // this id. mergePlaybookCommits returns commits sorted
          // newest-first, so the first match is the active one.
          viewedCommit: pickActiveCommitSha(item.commits ?? [], item.source),
        });
        // Track this id in the session-recent list so the sidebar's Related
        // tray can offer back-navigation later.
        recordRecentPlaybook(id);
        // Seed sync state from the list response so the push-PR badge
        // is correct on first paint. The debounced preview effect
        // takes over once the operator starts editing.
        setSyncState(item.syncState);
        setSelectedNode(parsed.entrypoint);
        // No-op restore lets the persist effect skip its own write below.
        void restored;
      })
      .catch((e) => {
        if (cancelled) return;
        setLoad({
          kind: "error",
          message: e instanceof ApiError ? e.message : String(e),
        });
      });

    return () => {
      cancelled = true;
    };
  }, [id, refetchKey]);

  // Chat-alive gate: once the operator has opened a chat session for
  // some playbook, the drawer's session is anchored to THAT subject
  // server-side. The operator can navigate around — Related tray,
  // sidebar list, browser back/forward — without losing the chat
  // they're mid-conversation in. All chat-anchored UI state survives
  // the URL change: drawer open/collapsed, the chat's subject snapshot,
  // queued proposal tabs (drafted by the same session), and the
  // viewMode that may currently be on one of those tabs.
  //
  // When chat is NOT alive, URL changes reset the per-subject UI as
  // before — there's nothing to preserve.
  useEffect(() => {
    if (chatPlaybookId !== null) return;
    setChatOpen(false);
    setChatVersion(null);
    setChatPlaybookId(null);
    setChatSessionId(null);
    setPendingProposals(new Map());
    setDismissedProposalIDs(new Set());
    setViewMode((vm) =>
      vm.kind === "proposal" ? { kind: "graph" } : vm,
    );
    // viewMode + chatPlaybookId are intentionally not in the dep list —
    // we only want to bounce on id change. The chatPlaybookId guard is
    // a runtime read of the current value at the moment id changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  // Tool catalog + capabilities — fetched once. The PR-push button gates
  // on capabilities.gh.authenticated && capabilities.repoPath.valid.
  useEffect(() => {
    api.listTools().then(setTools).catch(() => setTools([]));
    api
      .listPlaybookTypes()
      .then((types) =>
        setAvailableTypes(
          types.map((t) => ({ name: t.name, description: t.description })),
        ),
      )
      .catch(() => setAvailableTypes([]));
    api.getCapabilities().then(setCapabilities).catch(() => setCapabilities(null));
  }, []);

  // Playbook id index — refreshed when the parent bumps onMutated (a
  // sibling save/delete might have changed the available targets).
  useEffect(() => {
    api
      .listPlaybooks()
      .then((list) => setAllPlaybookIds(list.map((p) => p.id)))
      .catch(() => setAllPlaybookIds([]));
  }, [id]);

  // Persist drafts to localStorage *only when the draft has actually
  // diverged from the loaded original*. This avoids the "you have
  // unsaved changes" prompt firing after a passive open-then-leave —
  // before this guard, the initial dispatch({type:"set",next:parsed})
  // counted as an "edit" and persisted an identical copy.
  useEffect(() => {
    if (load.kind !== "loaded") return;
    const next = JSON.stringify(draft);
    const original = JSON.stringify(load.original);
    if (next === original) {
      // Roundtripped to clean — clear any prior draft so subsequent
      // visits don't see stale state.
      window.localStorage.removeItem(LOCAL_DRAFT_PREFIX + draft.id);
      return;
    }
    try {
      window.localStorage.setItem(LOCAL_DRAFT_PREFIX + draft.id, next);
    } catch {
      /* localStorage full or disabled */
    }
  }, [draft, load]);

  const yaml = useMemo(() => {
    try {
      return dumpPlaybookYAML(draft);
    } catch (e) {
      return `# YAML serialisation failed: ${e instanceof Error ? e.message : String(e)}`;
    }
  }, [draft]);

  // Live drift preview. Whenever the operator's draft yaml changes we
  // ask the launcher "would this body be a no-op push against
  // upstream?", debounced 300ms so a fast typist doesn't hammer the
  // server. The same resolver runs the list endpoint, so this is the
  // contract that makes the editor's push-PR gate agree with the
  // sidebar's drift badge.
  //
  // We skip the call for new playbooks (no upstream to compare),
  // locked entries (read-only, no push surface), and the loading
  // state. Network failures are swallowed — the previous syncState
  // stays put, which is more useful than blanking the badge on a
  // transient error.
  const livePreviewEnabled =
    load.kind === "loaded" && !load.isNew && !load.locked;
  useEffect(() => {
    if (!livePreviewEnabled) {
      return;
    }
    let cancelled = false;
    const handle = setTimeout(() => {
      api
        .previewPlaybookSyncState(
          draft.id,
          yaml,
          draft.type ?? "investigation",
        )
        .then((next) => {
          if (!cancelled) setSyncState(next);
        })
        .catch(() => {
          /* leave previous syncState; transient errors aren't worth
             clearing the operator's gate over */
        });
    }, 300);
    return () => {
      cancelled = true;
      clearTimeout(handle);
    };
  }, [livePreviewEnabled, yaml, draft.id, draft.type]);

  const clientErrors = useMemo(() => validate(draft), [draft]);
  const allErrors = useMemo(
    () => [...serverErrors, ...clientErrors],
    [serverErrors, clientErrors],
  );
  // Dirty: any edit since the last load/save. bodyDirty strips
  // active+version from both sides since those are stamped at write
  // time, not operator intent. activeDirty tracks the enable/disable
  // toggle separately so the dirty marker fires even when the body is
  // otherwise unchanged. Both feed into dirty, which gates the save
  // button and the save() callback.
  const stripActive = (p: Playbook): Omit<Playbook, "active"> => {
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const { active, ...rest } = p;
    return rest;
  };
  // Normalise active: undefined or true ⇒ "active"; only an explicit
  // false counts as disabled. Matches the loader's interpretation
  // (missing active field means the playbook ships enabled).
  const normaliseActive = (a: boolean | undefined): boolean => a !== false;
  const bodyDirty = useMemo(
    () =>
      load.kind === "loaded" &&
      JSON.stringify(stripActive(draft)) !==
        JSON.stringify(stripActive(load.original)),
    [draft, load],
  );
  const activeDirty = useMemo(
    () =>
      load.kind === "loaded" &&
      normaliseActive(draft.active) !== normaliseActive(load.original.active),
    [draft, load],
  );
  const dirty = bodyDirty || activeDirty;

  // Locked playbooks (system tier — bundled with the launcher) are
  // fully read-only: no save, no delete, no type change, no
  // push-PR. The source column was previously the lock signal
  // (source === "system") since that was the only un-editable case;
  // now we read it off the explicit `locked` flag so plugin-tier
  // entries (overridable) stay editable while system metas don't.
  const readOnly = load.kind === "loaded" && load.locked;

  const save = useCallback(async (msg?: string) => {
    if (load.kind !== "loaded") return;
    if (!dirty) return;
    setSaving(true);
    setServerErrors([]);
    // Use the explicitly passed message (from SaveDialog), falling back
    // to the commitMessage state (kept for any programmatic callers).
    const effectiveMsg = msg !== undefined ? msg : commitMessage;
    try {
      const errs = await api.savePlaybook(
        draft.id,
        yaml,
        draft.type ?? "investigation",
        normaliseActive(draft.active),
        effectiveMsg.trim() || undefined,
      );
      if (errs && errs.length > 0) {
        setServerErrors(errs);
        return;
      }
      window.localStorage.removeItem(LOCAL_DRAFT_PREFIX + draft.id);
      setCommitMessage("");
      setShowSaveDialog(false);
      if (typeof window !== "undefined") {
        window.dispatchEvent(new Event("c1:playbooks-changed"));
      }
      onMutated();
      // Stay on the editor — refetch so commits[] picks up the new
      // local commit and the version dropdown reflects the freshly-
      // written active version. Operators wanted in-place save; the
      // previous onBack() bounced them out to the list which felt
      // like a redirect.
      setRefetchKey((k) => k + 1);
    } catch (e) {
      setServerErrors([e instanceof ApiError ? e.message : String(e)]);
    } finally {
      setSaving(false);
    }
  }, [load, draft, yaml, dirty, commitMessage, onMutated]);

  // Toggle the active flag in the draft. Doesn't persist — the user
  // must hit Save to commit. Replaces the previous direct-API path
  // so the toggle behaves like every other field edit (dirty marker,
  // discardable, push-PR gating).
  const toggleActive = useCallback(() => {
    dispatch({
      type: "setMeta",
      meta: { active: !normaliseActive(draft.active) },
    });
  }, [draft.active]);

  const discard = useCallback(async () => {
    if (load.kind !== "loaded") return;
    const ok = await dialog.confirm({
      title: "Discard your edits?",
      body: "Reload the saved version. Anything in your in-progress draft will be lost.",
      confirmLabel: "Discard",
      danger: true,
    });
    if (!ok) return;
    window.localStorage.removeItem(LOCAL_DRAFT_PREFIX + draft.id);
    dispatch({ type: "set", next: load.original });
    setServerErrors([]);
  }, [dialog, draft.id, load]);

  const remove = useCallback(async () => {
    const ok = await dialog.confirm({
      title: `Delete user version of "${draft.id}"?`,
      body: "If a system playbook with this id exists, it will become visible again on the next investigation.",
      confirmLabel: "Delete",
      danger: true,
    });
    if (!ok) return;
    try {
      await api.deletePlaybook(draft.id);
      window.localStorage.removeItem(LOCAL_DRAFT_PREFIX + draft.id);
      if (typeof window !== "undefined") {
        window.dispatchEvent(new Event("c1:playbooks-changed"));
      }
      onMutated();
      onBack();
    } catch (e) {
      await dialog.alert({
        title: "Delete failed",
        body: e instanceof ApiError ? e.message : String(e),
      });
    }
  }, [dialog, draft.id, onMutated, onBack]);

  // pickCommit loads the playbook body at a historical commit into the
  // editor as a draft. If there are unsaved edits the operator is
  // prompted to confirm before discarding them.
  const pickCommit = useCallback(async (commit: PlaybookCommit) => {
    if (load.kind !== "loaded") return;
    if (dirty) {
      const ok = await dialog.confirm({
        title: `Switch to commit ${commit.sha.slice(0, 7)}?`,
        body: "You have unsaved edits. Switching will discard them.",
        confirmLabel: "Switch + discard",
        danger: true,
      });
      if (!ok) return;
    }
    try {
      const v = await api.getPlaybookAtCommit(draft.id, commit.sha);
      const parsed = parsePlaybookYAML(v.yaml);
      dispatch({ type: "set", next: parsed });
      setLoad({ ...load, original: parsed, viewedCommit: commit.sha });
      setSelectedNode(parsed.entrypoint);
    } catch (e) {
      await dialog.alert({
        title: "Failed to load commit",
        body: e instanceof ApiError ? e.message : String(e),
      });
    }
  }, [dialog, draft.id, load, dirty]);

  // pickLatest resets the editor to the active head: the YAML
  // currently on disk + the matching commit selected in the
  // dropdown. Triggered by the bare "latest" option in CommitsDropdown
  // — without this the option used to be a dead end (the dropdown
  // would visually flash to "latest" but the editor body stayed on
  // whatever historical commit you'd previously picked).
  const pickLatest = useCallback(async () => {
    if (load.kind !== "loaded") return;
    if (dirty) {
      const ok = await dialog.confirm({
        title: "Switch to latest?",
        body: "You have unsaved edits. Switching will discard them.",
        confirmLabel: "Switch + discard",
        danger: true,
      });
      if (!ok) return;
    }
    try {
      const item = await api.getPlaybook(draft.id);
      const parsed = parsePlaybookYAML(item.yaml);
      if (item.type) parsed.type = item.type;
      dispatch({ type: "set", next: parsed });
      setLoad({
        ...load,
        original: parsed,
        commits: item.commits ?? load.commits,
        upstreamYAML: item.upstreamYAML,
        source: item.source,
        viewedCommit: pickActiveCommitSha(
          item.commits ?? load.commits,
          item.source,
        ),
      });
      // Refresh sync state too — switching to latest can move the
      // editor onto a body that's now byte-equal to upstream.
      setSyncState(item.syncState);
      setSelectedNode(parsed.entrypoint);
    } catch (e) {
      await dialog.alert({
        title: "Failed to load latest",
        body: e instanceof ApiError ? e.message : String(e),
      });
    }
  }, [dialog, draft.id, load, dirty]);

  // Disable is now a draft action — see toggleActive above. The
  // direct-API disable path was removed when we unified the
  // enable/disable toggle into the dirty/save flow.

  // Stable handler for the drawer's onPendingProposalsChange. Using
  // useCallback with [] avoids a render loop: an inline arrow would have a
  // fresh identity each render, causing the drawer's lift useEffect to re-fire
  // and call setPendingProposals(new Map()) unconditionally. The new
  // Map-shaped state has no Object.is bail-out (unlike the prior null
  // primitive), so every emit would trigger a re-render → fresh handler →
  // loop. All state reads go through pendingProposalsRef or functional
  // setters, so the [] dep array is safe.
  const handleDrawerProposals = useCallback(
    (payloads: (ProposalDraftPayload | WikiProposalPayload)[]) => {
      // Build the Map straight from the transcript projection. Visibility
      // is filtered downstream by `proposalArray`'s useMemo, so the Map
      // can carry dismissed entries silently — they just never reach the
      // tab strip. Avoids a stale-closure race between the drawer's
      // SSE-driven useEffect and our dismissedProposalIDs state updates.
      const playbookPayloads = payloads as ProposalDraftPayload[];
      const nextMap = new Map(playbookPayloads.map((p) => [p.playbook_id, p]));
      const priorMap = pendingProposalsRef.current;
      setPendingProposals(nextMap);
      // Migrate viewMode if the active proposal_id was just replaced by
      // a re-draft for the same playbook_id. Without this, the body
      // bounces to ProposalTabBody's "no longer pending" fallback even
      // though the tab chip is right there with the live diff.
      setViewMode((vm) => {
        if (vm.kind !== "proposal") return vm;
        if (playbookPayloads.some((p) => p.proposal_id === vm.proposalId))
          return vm; // active tab still alive, no migration needed
        // Find the playbook_id that owned the now-stale proposal_id.
        const stalePlaybookID = Array.from(priorMap.entries()).find(
          ([, p]) => p.proposal_id === vm.proposalId,
        )?.[0];
        if (!stalePlaybookID) return vm; // active was already discarded
        const replacement = nextMap.get(stalePlaybookID);
        return replacement
          ? { kind: "proposal", proposalId: replacement.proposal_id }
          : vm;
      });
    },
    [],
  );

  // Flags for the unified return below. We no longer use early returns
  // so that the chat drawer + floating reopen button can remain mounted
  // across load.kind === "loading" transitions (e.g. after approving a
  // proposal from __new mode navigates the editor onto a saved playbook
  // id). Without this, the early return would unmount the drawer,
  // dropping its local transcript and pendingProposals state.
  const showLoading = load.kind === "loading";
  const showError = load.kind === "error";
  const showContent = load.kind === "loaded";

  return (
    <div data-testid="triagent-playbook-editor" className="flex h-full flex-col gap-3 px-6 py-6">
      {showLoading && (
        <div className="flex items-center gap-2 text-sm text-zinc-400">
          <Spinner /> loading playbook…
        </div>
      )}
      {showError && load.kind === "error" && (
        <div className="space-y-3">
          <div className="rounded border border-red-900/60 bg-red-950/40 p-3 text-sm text-red-200/90">
            {load.message}
          </div>
          <button
            type="button"
            onClick={onBack}
            className="inline-flex items-center gap-1.5 text-sm text-zinc-400 transition hover:text-zinc-200"
          >
            <ArrowLeftIcon className="h-3.5 w-3.5" />
            back to playbooks
          </button>
        </div>
      )}
      {showContent && load.kind === "loaded" && (() => {
        const allNodeIds = Object.keys(draft.nodes);
        const node = selectedNode ? draft.nodes[selectedNode] ?? null : null;
        const currentType =
          draft.type ?? availableTypes[0]?.name ?? "investigation";
        const typeDescription =
          availableTypes.find((t) => t.name === currentType)?.description ?? "";
        return (<>
      {/* Header — two rows: identity (row 1) + actions (row 2) */}
      <header className="flex flex-col gap-1">
        {/* Row 1: back link + id + symptom + type controls */}
        <div className="min-w-0 space-y-1">
          <button
            type="button"
            onClick={onBack}
            className="inline-flex items-center gap-1.5 text-sm text-zinc-400 transition hover:text-zinc-200"
          >
            <ArrowLeftIcon className="h-3.5 w-3.5" />
            back to playbooks
          </button>
          <input
            value={draft.id}
            disabled={readOnly}
            onChange={(e) =>
              dispatch({ type: "setMeta", meta: { id: e.target.value } })
            }
            className="w-full bg-transparent font-mono text-xl font-semibold tracking-tight text-zinc-100 focus:outline-none disabled:text-zinc-300"
            placeholder="playbook_id"
          />
          <input
            value={draft.symptom}
            disabled={readOnly}
            onChange={(e) =>
              dispatch({ type: "setMeta", meta: { symptom: e.target.value } })
            }
            className="w-full bg-transparent text-sm text-zinc-400 focus:outline-none disabled:text-zinc-500"
            placeholder="symptom that triggers this playbook"
          />
        </div>
        {/* Row 2: actions — split into a left status/identity group and a
            right actions group, with justify-between pushing them apart. */}
        <div className="mt-2 flex flex-wrap items-center justify-between gap-2 border-t border-zinc-800/60 pt-2">
          {/* LEFT GROUP — status / identity info about the playbook */}
          <div className="flex flex-wrap items-center gap-2">
            <SourceBadge source={load.source} />
            {/* Active toggle — draft.active is the live draft field; the
                operator must Save to commit the change. */}
            {!load.isNew && (
              readOnly
                ? (
                  <span
                    aria-label={load.disabled ? "disabled" : "active"}
                    className={
                      "inline-flex items-center gap-2 rounded-full border px-2.5 py-1 text-xs font-medium uppercase tracking-wide " +
                      (load.disabled
                        ? "border-zinc-700 bg-zinc-800/60 text-zinc-400"
                        : "border-emerald-700/60 bg-emerald-900/40 text-emerald-300")
                    }
                  >
                    <span
                      aria-hidden
                      className={
                        "relative inline-block h-3 w-6 rounded-full " +
                        (load.disabled ? "bg-zinc-700" : "bg-emerald-500/70")
                      }
                    >
                      <span
                        className={
                          "absolute top-[2px] h-2 w-2 rounded-full bg-zinc-100 shadow " +
                          (load.disabled ? "left-[2px]" : "left-[14px]")
                        }
                      />
                    </span>
                    <span className={load.disabled ? "line-through decoration-zinc-600" : ""}>
                      {load.disabled ? "disabled" : "active"}
                    </span>
                  </span>
                )
                : (
                  <button
                    type="button"
                    onClick={() => void toggleActive()}
                    role="switch"
                    aria-checked={normaliseActive(draft.active)}
                    title={
                      !normaliseActive(draft.active)
                        ? "stage enable — Save to commit"
                        : "stage disable — Save to commit; YAML stays on disk"
                    }
                    className={
                      "group inline-flex items-center gap-2 rounded-full border px-2.5 py-1 text-xs font-medium uppercase tracking-wide transition " +
                      (!normaliseActive(draft.active)
                        ? "border-zinc-700 bg-zinc-800/60 text-zinc-400 hover:border-emerald-700/60 hover:text-zinc-200"
                        : "border-emerald-700/60 bg-emerald-900/40 text-emerald-300 hover:border-emerald-500 hover:bg-emerald-900/60")
                    }
                  >
                    <span
                      aria-hidden
                      className={
                        "relative inline-block h-3 w-6 rounded-full transition " +
                        (!normaliseActive(draft.active)
                          ? "bg-zinc-700 group-hover:bg-zinc-600"
                          : "bg-emerald-500/70 group-hover:bg-emerald-400/80")
                      }
                    >
                      <span
                        className={
                          "absolute top-[2px] h-2 w-2 rounded-full bg-zinc-100 shadow transition-all " +
                          (!normaliseActive(draft.active) ? "left-[2px]" : "left-[14px]")
                        }
                      />
                    </span>
                    <span className={!normaliseActive(draft.active) ? "line-through decoration-zinc-600" : ""}>
                      {!normaliseActive(draft.active) ? "disabled" : "active"}
                    </span>
                  </button>
                )
            )}
            {/* Type select — the directory slot the playbook lives in;
                moving changes which type-dir the file is written to on
                the next Save. Description shown as a hover tooltip. */}
            <select
              value={
                draft.type ??
                (availableTypes[0]?.name ?? "investigation")
              }
              // Type slot is the directory the file lives in. For
              // existing playbooks, changing it moves the single file
              // from the old type dir to the new one on the next save.
              // Only the read-only flag locks the field.
              disabled={readOnly}
              title={typeDescription || `type slot: ${currentType}`}
              onChange={async (e) => {
                const next = e.target.value;
                // Confirm before changing for an existing playbook
                // — the move deletes the file from the old type dir
                // and writes it to the new one, so this isn't a
                // pure cosmetic edit.
                if (
                  !load.isNew &&
                  load.original.type &&
                  next !== load.original.type
                ) {
                  const ok = await dialog.confirm({
                    title: "Change playbook type?",
                    body: `This moves "${draft.id}" from "${load.original.type}" to "${next}". The file in the old slot is removed. Continue?`,
                    confirmLabel: "Change type",
                    cancelLabel: "Keep current",
                  });
                  if (!ok) return;
                }
                dispatch({
                  type: "setMeta",
                  meta: { type: next },
                });
              }}
              className="rounded border border-zinc-800 bg-zinc-950 px-1.5 py-0.5 text-xs text-zinc-200 focus:border-zinc-600 focus:outline-none disabled:opacity-60"
            >
              {availableTypes.length === 0 && (
                <option value="investigation">investigation</option>
              )}
              {availableTypes.map((t) => (
                <option key={t.name} value={t.name}>
                  {t.name}
                </option>
              ))}
              {/* Surface the current type as an option even when
                  the upstream type catalog doesn't list it.
                  Without this, a playbook in a slot that's not in
                  availableTypes (system metas live in the bundled
                  `system/` dir, not the upstream clone; operator
                  subdirs like `investigation-wip` similarly) would
                  fall back to the first <option> visually because
                  the select's value has no match — i.e. system
                  metas would render as "investigation". */}
              {draft.type &&
                !availableTypes.some((t) => t.name === draft.type) && (
                  <option key={draft.type} value={draft.type}>
                    {draft.type}
                  </option>
                )}
            </select>
            {/* Commits dropdown — shows merged upstream+local history,
                lazy-loads older entries, selecting a commit loads that
                body as a draft. Hidden for brand-new unsaved playbooks. */}
            {!load.isNew && (
              <CommitsDropdown
                playbookID={draft.id}
                commits={load.commits}
                currentSha={load.viewedCommit}
                activeSha={pickActiveCommitSha(load.commits, load.source)}
                onPickCommit={pickCommit}
                onPickLatest={pickLatest}
                dirty={dirty}
              />
            )}
          </div>

          {/* RIGHT GROUP — action buttons */}
          <div className="flex flex-wrap items-center gap-2">
            {!readOnly && (
              <>
                <button
                  type="button"
                  onClick={discard}
                  disabled={!dirty}
                  className={dirty ? BTN_SECONDARY : BTN_GATED}
                  title={
                    dirty
                      ? "discard your unsaved edits and reload the saved version"
                      : "no unsaved edits to discard"
                  }
                >
                  <RevertIcon className="h-3.5 w-3.5" />
                  discard edits
                </button>
                {load.source === "user" || load.source === "override" ? (
                  <button
                    type="button"
                    onClick={remove}
                    className={BTN_DANGER}
                    title="delete the saved user version of this playbook from disk"
                  >
                    <TrashIcon className="h-3.5 w-3.5" />
                    delete playbook
                  </button>
                ) : null}
                <button
                  type="button"
                  onClick={() => setShowSaveDialog(true)}
                  disabled={saving || allErrors.length > 0 || !dirty}
                  title={
                    !dirty && !saving && allErrors.length === 0
                      ? "no changes to save"
                      : undefined
                  }
                  className={BTN_PRIMARY}
                >
                  {saving
                    ? "saving…"
                    : load.source === "plugin"
                      ? "save locally"
                      : "save"}
                </button>
              </>
            )}
            {!readOnly && (
              <PushPRButton
                capabilities={capabilities}
                disabled={allErrors.length > 0}
                // Server-authoritative drift answer. "synced" means a
                // push would be a no-op — same rule the list applies,
                // so the editor and the sidebar can't disagree about
                // whether there's anything to push.
                noUpstreamDiff={syncState?.status === "synced"}
                onClick={() => setShowPRModal(true)}
              />
            )}
            <ChatToggleButton
              open={chatOpen}
              onClick={() => {
                if (chatOpen) {
                  setChatOpen(false);
                  return;
                }
                // Snapshot the commit the drawer should be frozen to.
                // Uses the currently-viewed commit sha, or "HEAD" for
                // brand-new and latest-HEAD playbooks. Pair the id
                // snapshot so an in-flight chat survives a draft.id
                // change (e.g. accepting an AI proposal from __new
                // navigates the editor onto the freshly-saved file).
                setChatVersion(load.viewedCommit ?? "HEAD");
                setChatPlaybookId(draft.id);
                // Snapshot isNew + yaml + type at chat-open time so
                // the drawer subject stays stable across load.kind
                // transitions (the __new → saved-id URL navigation
                // briefly sets load.kind to "loading", making these
                // fields inaccessible from live state).
                setChatIsNew(load.kind === "loaded" ? load.isNew : false);
                setChatYaml(yaml);
                setChatType(draft.type);
                setChatOpen(true);
              }}
              label={load.isNew ? "draft with AI" : "chat"}
            />
          </div>
        </div>
      </header>

      {showPRModal && capabilities && (
        <PushPRModal
          playbookID={draft.id}
          yaml={yaml}
          symptom={draft.symptom}
          type={draft.type ?? "investigation"}
          capabilities={capabilities}
          onClose={() => setShowPRModal(false)}
        />
      )}

      {showSaveDialog && (
        <SaveDialog
          playbookID={draft.id}
          dirty={dirty}
          onConfirm={(msg) => save(msg)}
          onCancel={() => {
            setShowSaveDialog(false);
            setServerErrors([]);
          }}
          saving={saving}
          errors={serverErrors}
        />
      )}

      {/* Entity tags moved into the right aside under the "tags"
          sub-tab so the graph workspace reclaims its full vertical
          space. See <aside> below. */}

      {/* Main: tabbed graph/YAML pane + node side panel. The chat
          drawer covers the bottom of the viewport when open, so we
          show graph and YAML as tabs (one at a time) instead of
          stacked — gives the active view the full vertical space. */}
      <div className="flex min-h-0 flex-1 gap-3">
        <div className="flex flex-1 flex-col gap-2 min-w-0">
          <div className="flex items-center gap-1 overflow-x-auto border-b border-zinc-800 pl-1">
            <ViewTab
              active={viewMode.kind === "graph"}
              onClick={() => setViewMode({ kind: "graph" })}
              label="diagram"
            />
            <ViewTab
              active={viewMode.kind === "yaml"}
              onClick={() => setViewMode({ kind: "yaml" })}
              label="yaml"
              badge={
                allErrors.length > 0 ? (
                  <span className="rounded-full bg-red-900/60 px-1.5 py-0.5 text-xs font-medium text-red-200">
                    {allErrors.length}
                  </span>
                ) : yaml.trim().length > 0 ? (
                  <span
                    title="YAML valid"
                    aria-hidden
                    className="inline-block h-1.5 w-1.5 rounded-full bg-emerald-400"
                  />
                ) : null
              }
            />
            {proposalArray.map((p) => (
              <ProposalTab
                key={p.proposal_id}
                payload={p}
                active={
                  viewMode.kind === "proposal" &&
                  viewMode.proposalId === p.proposal_id
                }
                onClick={() =>
                  setViewMode({ kind: "proposal", proposalId: p.proposal_id })
                }
                onDiscard={discardProposal}
              />
            ))}
          </div>
          <div className="min-h-0 flex-1">
            {viewMode.kind === "graph" && (
              <PlaybookGraph
                playbook={draft}
                selectedId={selectedNode}
                onSelect={setSelectedNode}
                onOpenPlaybook={onOpenPlaybook}
              />
            )}
            {viewMode.kind === "yaml" && (
              <YamlPanel
                yaml={yaml}
                errors={allErrors}
                collapsed={false}
                embedded
                onToggleCollapsed={() => {
                  /* tab-mode is always-expanded; collapse is N/A */
                }}
              />
            )}
            {viewMode.kind === "proposal" && (
              <ProposalTabBody
                payload={proposalArray.find(
                  (p) => p.proposal_id === viewMode.proposalId,
                )}
                onResolved={onProposalResolved}
              />
            )}
          </div>
        </div>

        <aside className="flex w-96 shrink-0 flex-col rounded border border-zinc-800 bg-zinc-900/30">
          {/* Tabbed sticky header: switch between per-node editing
              (the dominant per-iteration surface) and playbook-level
              entity tags (set once, revisited rarely). The trailing
              "+ add node" action only makes sense for the node tab. */}
          <header className="flex items-stretch justify-between border-b border-zinc-800 bg-zinc-900/60">
            <div className="flex">
              <button
                type="button"
                onClick={() => setAsideTab("node")}
                aria-pressed={asideTab === "node"}
                className={`px-3 py-2 text-sm transition ${
                  asideTab === "node"
                    ? "border-b-2 border-sky-500 text-zinc-100"
                    : "text-zinc-400 hover:text-zinc-200"
                }`}
              >
                node
              </button>
              <button
                type="button"
                onClick={() => setAsideTab("tags")}
                aria-pressed={asideTab === "tags"}
                className={`px-3 py-2 text-sm transition ${
                  asideTab === "tags"
                    ? "border-b-2 border-sky-500 text-zinc-100"
                    : "text-zinc-400 hover:text-zinc-200"
                }`}
              >
                tags
              </button>
            </div>
            {asideTab === "node" && !readOnly && (
              <button
                type="button"
                onClick={async () => {
                  const newId = await dialog.prompt({
                    title: "Add node",
                    body: "Pick an id for the new node. It can be referenced by other nodes' goto branches.",
                    placeholder: "snake_case_id",
                    confirmLabel: "Add",
                    validate: (v) => {
                      const trimmed = v.trim();
                      if (!trimmed) return "id is required";
                      if (draft.nodes[trimmed])
                        return `node "${trimmed}" already exists`;
                      return null;
                    },
                  });
                  if (!newId) return;
                  const trimmed = newId.trim();
                  dispatch({ type: "addNode", nodeId: trimmed });
                  setSelectedNode(trimmed);
                }}
                className="m-1 self-center rounded border border-dashed border-zinc-700 px-2 py-1 text-xs text-zinc-300 transition hover:border-zinc-500 hover:bg-zinc-800/50 hover:text-zinc-100"
              >
                + add node
              </button>
            )}
          </header>
          <div className="min-h-0 flex-1 space-y-3 overflow-y-auto p-3">
            {asideTab === "node" ? (
              <NodeEditor
                nodeId={selectedNode}
                node={node}
                allNodeIds={allNodeIds}
                allPlaybookIds={allPlaybookIds.filter((p) => p !== draft.id)}
                catalog={tools}
                readOnly={readOnly}
                onChange={(next) => {
                  if (!selectedNode) return;
                  dispatch({ type: "setNode", nodeId: selectedNode, node: next });
                }}
                onRename={(newId) => {
                  if (!selectedNode) return;
                  dispatch({ type: "renameNode", oldId: selectedNode, newId });
                  setSelectedNode(newId);
                }}
                onDelete={() => {
                  if (!selectedNode) return;
                  dispatch({ type: "deleteNode", nodeId: selectedNode });
                  setSelectedNode(null);
                }}
                onOpenPlaybook={onOpenPlaybook}
              />
            ) : (
              <div className="space-y-3">
                <p className="text-xs text-zinc-500">
                  Canonical service / error / symptom names used by{" "}
                  <span className="font-mono">playbook_correlate</span> to
                  rank related playbooks. Use the same vocabulary as the
                  wiki vault — lowercase, hyphens only (e.g.{" "}
                  <code className="font-mono">zeebe-broker</code>).
                </p>
                <EntityChipsInput
                  label="services"
                  values={draft.services ?? []}
                  onChange={(next) =>
                    dispatch({ type: "setMeta", meta: { services: next } })
                  }
                />
                <EntityChipsInput
                  label="errors"
                  values={draft.errors ?? []}
                  onChange={(next) =>
                    dispatch({ type: "setMeta", meta: { errors: next } })
                  }
                />
                <EntityChipsInput
                  label="symptoms"
                  values={draft.symptoms ?? []}
                  onChange={(next) =>
                    dispatch({ type: "setMeta", meta: { symptoms: next } })
                  }
                />
              </div>
            )}
          </div>
          {/* Correlation ranking moved out of the aside into the
              sidebar (PlaybookCorrelationPanel in Sidebar.tsx) — it's
              reference content rather than active editing surface, so
              it co-locates better with the playbook navigation. The
              aside now stays focused on the node / tags editor. */}
        </aside>
      </div>
      </>);
      })()}

      {/* Chat drawer + floating reopen button — always rendered when
          chat is alive, regardless of editor body load state. The
          drawer is fixed-position so it doesn't affect document
          layout; keeping it here (outside the showContent branch)
          lets its useEditorSession run continuously across editor URL
          transitions. Without this, the body's load.kind === "loading"
          state would unmount the drawer, dropping its local transcript
          and pendingProposals state. */}
      {chatOpen && chatVersion && chatPlaybookId && (
        <EditorChatDrawer
          open={chatOpen}
          subject={{
            kind: "playbook",
            playbook: {
              // Locked snapshot, NOT draft.id — see chatPlaybookId
              // declaration. draft.id can change mid-session (operator
              // edits the id field, or the editor reloads onto a saved
              // playbook after an accepted proposal); the chat subject
              // must stay pinned to whatever id was in play when the
              // backend session was created.
              id: chatPlaybookId,
              version: chatVersion,
              // For the "+ new playbook" flow there's nothing on disk
              // for the backend to load — pass the in-progress draft
              // YAML so the agent sees what the operator has typed
              // (symptom, type slot, any starter nodes) instead of an
              // empty stub. isNew tells the backend to skip the
              // file-system lookup entirely.
              // Snapshots from chat-open time. Stay stable across editor
              // body load transitions so the drawer doesn't have to
              // remount when the URL navigates.
              ...(chatIsNew
                ? { isNew: true, yaml: chatYaml, type: chatType }
                : null),
            },
          }}
          onCollapse={() => setChatOpen(false)}
          onTerminate={() => {
            setChatOpen(false);
            setChatVersion(null);
            setChatPlaybookId(null);
            setChatSessionId(null);
            setChatIsNew(false);
            setChatYaml("");
            setChatType(undefined);
            setPendingProposals(new Map());
            setDismissedProposalIDs(new Set());
            // Bounce out of proposal mode so terminating with a proposal tab
            // active doesn't strand the operator on the fallback copy.
            setViewMode((vm) => (vm.kind === "proposal" ? { kind: "graph" } : vm));
          }}
          proposalParser={parsePlaybookProposal}
          onPendingProposalsChange={handleDrawerProposals}
          onSessionIdChange={setChatSessionId}
          onProposalResolved={onProposalResolved}
          dismissedProposalIDs={dismissedProposalIDs}
        />
      )}
      {/* Floating reopen blob: visible when the chat session is
          alive but collapsed (drawer toggled off without the X
          button). Click reopens the same session — the toolbar
          button does the same thing, but the blob is closer-to-hand
          when the operator is mid-edit and the toolbar's scrolled
          out of focus. */}
      {!chatOpen && chatVersion && (
        <button
          type="button"
          onClick={() => setChatOpen(true)}
          title="resume chat session"
          aria-label="resume chat"
          className="fixed bottom-6 right-6 z-30 inline-flex h-12 w-12 items-center justify-center rounded-full bg-sky-600 text-white shadow-lg transition hover:bg-sky-500 hover:shadow-xl"
        >
          <ChatBubbleIcon className="h-5 w-5" />
          {/* Pulsing dot mirrors the chat status indicator vibe so
              the operator notices it's an active, paused
              conversation rather than a brand-new one. */}
          <span
            aria-hidden
            className="absolute right-1 top-1 h-2 w-2 animate-pulse rounded-full bg-emerald-400 ring-2 ring-zinc-950"
          />
        </button>
      )}
    </div>
  );
}

function ViewTab({
  active,
  onClick,
  label,
  badge,
  trailing,
  disabled,
}: {
  active: boolean;
  onClick: () => void;
  label: string;
  badge?: React.ReactNode;
  // trailing renders inside the same tab "chip" but to the right of
  // the label — used for the proposal tab's discard ✕. Kept inside
  // the chip rather than separate so the visual association is
  // unambiguous.
  trailing?: React.ReactNode;
  disabled?: boolean;
}) {
  return (
    <div
      role="tab"
      aria-selected={active}
      aria-disabled={disabled || undefined}
      className={
        "inline-flex shrink-0 items-center gap-2 rounded-t border-b-2 px-3 py-1.5 text-sm font-medium transition " +
        (disabled
          ? "cursor-not-allowed border-transparent text-zinc-700"
          : active
            ? "border-sky-500 bg-zinc-900/40 text-zinc-100"
            : "cursor-pointer border-transparent text-zinc-500 hover:text-zinc-200")
      }
      onClick={() => {
        if (disabled) return;
        onClick();
      }}
    >
      {label}
      {badge}
      {trailing}
    </div>
  );
}

function ProposalTabBody({
  payload,
  onResolved,
}: {
  payload: ProposalDraftPayload | undefined;
  onResolved: (
    p: ProposalDraftPayload,
    kind: "approved" | "declined",
  ) => void;
}) {
  if (!payload) {
    return (
      <div className="px-3 py-4 text-xs text-zinc-500">
        Proposal no longer pending — pick another tab.
      </div>
    );
  }
  return (
    <div className="h-full min-h-0 overflow-y-auto">
      <ProposalCard
        payload={payload}
        onSendRefinement={async () => {
          /* refinements go through the chat composer */
        }}
        onResolved={(kind) => onResolved(payload, kind)}
      />
    </div>
  );
}

function ProposalTab({
  payload,
  active,
  onClick,
  onDiscard,
}: {
  payload: ProposalDraftPayload;
  active: boolean;
  onClick: () => void;
  onDiscard: (p: ProposalDraftPayload) => void | Promise<void>;
}) {
  return (
    <div
      role="tab"
      aria-selected={active}
      title={payload.playbook_id}
      onClick={onClick}
      className={
        "inline-flex shrink-0 max-w-[14ch] items-center gap-1 rounded-t border-b-2 px-3 py-1.5 text-sm font-medium transition cursor-pointer " +
        (active
          ? "border-sky-500 bg-zinc-900/40 text-zinc-100"
          : "border-transparent text-zinc-500 hover:text-zinc-200")
      }
    >
      <span className="truncate">AI: {payload.playbook_id}</span>
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          void onDiscard(payload);
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
    </div>
  );
}

function ChatToggleButton({
  open,
  onClick,
  label = "chat",
}: {
  open: boolean;
  onClick: () => void;
  label?: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={open}
      title={
        open
          ? "collapse chat (keeps session alive — toggle again to resume)"
          : "open editor chat — refine this playbook with the assistant"
      }
      className={open ? BTN_SECONDARY_ACTIVE : BTN_SECONDARY}
    >
      <ChatBubbleIcon className="h-3.5 w-3.5" />
      {label}
    </button>
  );
}

// PushPRButton renders the "push as PR" action and explains via a
// tooltip when it's disabled (gh missing, repo path missing, validation
// errors). Disabled-but-visible is intentional — operators should
// understand the feature exists and what to fix to unlock it, rather
// than wondering why a button is missing. System (locked) playbooks
// don't render this at all — the parent gates it.
function PushPRButton({
  capabilities,
  disabled,
  noUpstreamDiff,
  onClick,
}: {
  capabilities: Capabilities | null;
  disabled: boolean;
  // True when the operator's draft body matches upstream (with
  // active+version stripped). Pushing would create a no-op PR, so we
  // lock the button and explain why.
  noUpstreamDiff: boolean;
  onClick: () => void;
}) {
  let blockReason: string | null = null;
  if (!capabilities) {
    blockReason = "checking capabilities…";
  } else if (!capabilities.gh.authenticated) {
    blockReason = capabilities.gh.reason ?? "gh CLI unavailable";
  } else if (!capabilities.repoPath.valid) {
    blockReason = capabilities.repoPath.reason ?? "repo path not configured";
  } else if (!(capabilities.repoPath.repo ?? "")) {
    // Local-only mode: the playbooks dir is a usable git checkout but
    // has no upstream remote, so a PR has nowhere to go.
    blockReason = "no upstream playbooks repo configured — set defaults.playbooks_repo in your profile and restart the launcher";
  } else if (disabled) {
    blockReason = "fix the validation errors first";
  } else if (noUpstreamDiff) {
    blockReason = "no diff against upstream — edit the playbook before pushing";
  }
  const isDisabled = blockReason !== null;
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={isDisabled}
      title={blockReason ?? "Push this playbook as a PR to your playbooks repo checkout"}
      className={isDisabled ? BTN_GATED : BTN_SECONDARY}
    >
      <GitHubIcon className="h-3.5 w-3.5" />
      push as PR
    </button>
  );
}

function SourceBadge({ source }: { source: SourceTag }) {
  // Provenance only. "plugin" = upstream library (overridable);
  // "system" = launcher-bundled meta (locked); "user"/"override" =
  // local; "broken" stays as-is. The "is this synced with remote?"
  // question is answered separately by the unsynced cloud-up icon
  // on the playbook list card.
  const label =
    source === "plugin"
      ? "remote"
      : source === "system"
        ? "system"
        : source === "broken"
          ? "broken"
          : "local";
  const cls = {
    plugin: "bg-zinc-800 text-zinc-300",
    // System metas are launcher-owned — slate sky tone signals "this
    // is the framework, not your edits".
    system: "bg-sky-900/50 text-sky-200",
    user: "bg-emerald-900/50 text-emerald-300",
    override: "bg-emerald-900/50 text-emerald-300",
    broken: "bg-red-900/60 text-red-300",
  }[source];
  return (
    <span
      className={
        "rounded-full px-2 py-0.5 text-xs font-medium uppercase tracking-wide " +
        cls
      }
    >
      {label}
    </span>
  );
}


