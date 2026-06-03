"use client";

import type React from "react";
import {
  memo,
  useCallback,
  useEffect,
  useReducer,
  useRef,
  useState,
} from "react";
import {
  api,
  ApiError,
  type EditorSession,
  type EditorSources,
  type EditorSubject,
  type StreamEnvelope,
} from "@/lib/api";
import {
  applyEvent,
  PROPOSE_PLAYBOOK_DRAFT_TOOL_NAME,
  type EventEnvelope,
  type SessionStatus,
  type TranscriptItem,
} from "@/lib/events";
import { useStream } from "@/lib/stream";
import { buildLatestPayloadsByKey } from "@/lib/proposal-projection";
import { Markdown } from "@/components/shared/Markdown";
import { ProposalCard, type ProposalDraftPayload } from "@/components/playbooks/ProposalCard";
import { Spinner } from "@/components/shared/Spinner";
import { ToolCard } from "@/components/shared/ToolCard";
import type { WikiProposalPayload } from "@/components/wiki/WikiProposalCard";

// Editor reducer wraps applyEvent with a synthetic "__reset__" action
// so we can clear the transcript when the operator switches playbook
// or terminates the session, without remounting the whole drawer.
type EditorAction = EventEnvelope | { kind: "__reset__" };
function editorReducer(
  items: TranscriptItem[],
  action: EditorAction,
): TranscriptItem[] {
  if (action.kind === "__reset__") return [];
  return applyEvent(items, action);
}

type Props = {
  open: boolean;
  // Subject identifies what's being edited — a playbook (id+version) or
  // a wiki entry (kind+id). The drawer is reused for both.
  subject: EditorSubject;
  // Sources are optional auxiliary feeds (Slack channel, incident.io
  // incident) attached to the session. Wiki sessions opened from
  // NewWikiEntryModal usually pass these; playbook sessions today
  // don't.
  sources?: EditorSources;
  // Tool name to watch for in the transcript when lifting the latest
  // pending proposal. Defaults to "playbook_proposal_draft" for
  // backward-compatibility with the playbook editor; wiki callers pass
  // "propose_wiki_draft".
  proposalToolName?: string;
  // Each call site passes a parser that validates + extracts the dedup
  // key for its own payload shape (playbook proposals key by
  // playbook_id; wiki proposals key by proposal_id since each wiki
  // session has at most one in flight). Returns null for payloads it
  // can't safely use — those are skipped, same as unparseable JSON.
  proposalParser: (raw: unknown) => { key: string; payload: ProposalDraftPayload | WikiProposalPayload } | null;
  // Collapse keeps the session alive server-side; the drawer hides
  // and the parent's "open" prop flips false. Reopening re-attaches
  // SSE to the same session.
  onCollapse: () => void;
  // Explicit termination — DELETE the session, drop transcript state.
  // The X button in the drawer header invokes this; the session can't
  // be resumed afterwards.
  onTerminate: () => void;
  // Lifts the latest pending proposal (one per distinct playbook_id /
  // wiki entry id) up to the parent editor. The array is the values
  // of a Map keyed by the parser's dedup key — re-drafts for the same
  // key replace in place; first-seen insertion order is preserved
  // across re-emits. Wiki sessions get an array of length ≤1 in
  // practice.
  onPendingProposalsChange?: (
    payloads: (ProposalDraftPayload | WikiProposalPayload)[],
  ) => void;
  // onProposalResolved fires after the operator approves or declines
  // a proposal from inside the chat drawer's transcript. The editor
  // host owns the post-resolution navigation + sidebar refresh.
  onProposalResolved?: (
    payload: ProposalDraftPayload,
    kind: "approved" | "declined",
  ) => void;
  // onSessionIdChange fires whenever the live session id changes
  // (null → string when the drawer creates/finds a session, or string
  // → null on terminate). The editor host needs this to drive the
  // server-side rebind on accept-from-__new.
  onSessionIdChange?: (sessionId: string | null) => void;
  // Parent's set of dismissed proposal_ids — passed to chat-side
  // ProposalCards so they can shade their action row when the same
  // proposal has been handled via the editor's tab.
  dismissedProposalIDs?: ReadonlySet<string>;
  // Placeholder copy shown in the empty transcript area (after the
  // session has started but before the agent has produced its first
  // event). Defaults to playbook-flavoured copy for backwards-compat;
  // wiki callers pass their own so the operator doesn't read "refine
  // this playbook" while the wiki agent is warming up.
  emptyHint?: React.ReactNode;
};

// EditorChatDrawer is the bottom-anchored chat surface of the playbook
// editor. Fixed-position so it floats above the editor's main panels
// regardless of where the operator scrolled. The session lifecycle
// (create-or-resume on open, SSE connection while open, server-side
// close on terminate / playbook switch) lives in useEditorSession;
// this component is just the UI shell around it.
//
// Height is operator-resizable via the top-edge drag handle and
// persisted across sessions in localStorage. Bounds: at least 200px
// (a useful minimum) and at most 90vh (always leave a peek of the
// editor underneath so the operator knows where they are).
const HEIGHT_KEY = "triagent.editor-chat.height";
const MIN_HEIGHT_PX = 220;
const FALLBACK_HEIGHT_PX = 360;

function readStoredHeight(): number {
  if (typeof window === "undefined") return FALLBACK_HEIGHT_PX;
  const raw = window.localStorage.getItem(HEIGHT_KEY);
  if (!raw) return FALLBACK_HEIGHT_PX;
  const n = Number.parseInt(raw, 10);
  return Number.isFinite(n) && n >= MIN_HEIGHT_PX ? n : FALLBACK_HEIGHT_PX;
}

export function EditorChatDrawer({
  open,
  subject,
  sources,
  proposalToolName = PROPOSE_PLAYBOOK_DRAFT_TOOL_NAME,
  proposalParser,
  onCollapse,
  onTerminate,
  onPendingProposalsChange,
  onProposalResolved,
  onSessionIdChange,
  dismissedProposalIDs,
  emptyHint,
}: Props) {
  const session = useEditorSession({ subject, sources, enabled: open });

  useEffect(() => {
    onSessionIdChange?.(session.id);
  }, [session.id, onSessionIdChange]);

  // Project the transcript into the latest payload per dedup key and
  // emit the values array. The pure helper lives in lib/proposal-
  // projection.ts so we can unit-test it without React. Each call site
  // supplies its own parser for its payload shape (playbook vs wiki).
  useEffect(() => {
    if (!onPendingProposalsChange) return;
    onPendingProposalsChange(
      buildLatestPayloadsByKey(session.items, proposalToolName, proposalParser),
    );
  }, [session.items, onPendingProposalsChange, proposalToolName, proposalParser]);
  const [height, setHeight] = useState<number>(FALLBACK_HEIGHT_PX);
  // Hydrate the persisted height once on mount. Doing it after first
  // paint avoids an SSR/CSR hydration mismatch — server renders the
  // fallback, client swaps in the stored value.
  useEffect(() => {
    setHeight(readStoredHeight());
  }, []);

  // Persist new heights at the end of a drag rather than on every
  // mousemove (avoids hammering localStorage during the resize).
  const persistHeight = useCallback((h: number) => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem(HEIGHT_KEY, String(h));
  }, []);

  if (!open) return null;

  return (
    <aside
      // Fixed bottom-anchored drawer. Width matches the editor's
      // usable column (max-w-7xl mirrors the editor's main wrapper)
      // so the chat sits under the form rather than spanning the full
      // window. Height is operator-controlled via the resize handle.
      style={{ height }}
      className="fixed inset-x-0 bottom-0 z-30 mx-auto flex w-full max-w-7xl flex-col border-t border-zinc-700 bg-zinc-950 shadow-2xl"
      role="region"
      aria-label="playbook editor chat"
    >
      <ResizeHandle
        height={height}
        onResize={setHeight}
        onCommit={persistHeight}
      />
      <DrawerHeader
        subject={subject}
        status={session.status}
        onCollapse={onCollapse}
        onTerminate={() => {
          void session.terminate();
          onTerminate();
        }}
      />
      <div className="flex flex-1 min-h-0 flex-col gap-3 px-4 py-3">
        <Transcript
          items={session.items}
          status={session.status}
          streamErr={session.error}
          dismissedProposalIDs={dismissedProposalIDs ?? null}
          emptyHint={emptyHint}
          onProposalResolved={onProposalResolved}
        />
        <Composer
          disabled={!session.id || session.status === "starting"}
          streaming={session.status === "streaming"}
          onSend={session.send}
        />
      </div>
    </aside>
  );
}

// ResizeHandle is a 6px-tall strip along the drawer's top edge. Drag
// upward to make the drawer taller, downward to shrink. Bounded to
// [MIN_HEIGHT_PX, viewport*0.9] so the drawer can never push itself
// off-screen or shrink below usefulness. Listeners attach to window
// (not the handle) so a fast drag that leaves the handle's hitbox
// doesn't break the resize.
function ResizeHandle({
  height,
  onResize,
  onCommit,
}: {
  height: number;
  onResize: (next: number) => void;
  onCommit: (final: number) => void;
}) {
  const dragStart = useRef<{ y: number; startHeight: number } | null>(null);

  const onMouseDown = (e: React.MouseEvent) => {
    e.preventDefault();
    dragStart.current = { y: e.clientY, startHeight: height };

    const onMove = (ev: MouseEvent) => {
      const start = dragStart.current;
      if (!start) return;
      const max = Math.floor(window.innerHeight * 0.9);
      const next = Math.max(
        MIN_HEIGHT_PX,
        Math.min(max, start.startHeight + (start.y - ev.clientY)),
      );
      onResize(next);
    };
    const onUp = () => {
      const start = dragStart.current;
      dragStart.current = null;
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
      // Snapshot the final size from the height ref by calling
      // onCommit with the latest known value. We can't read state
      // here directly, so the parent's setHeight + persistHeight
      // pair handles the commit on its next render.
      if (start) {
        // Use a trailing rAF so we're sure React has applied the
        // last onResize before we persist.
        requestAnimationFrame(() => {
          // Read straight off the DOM — the aside's bounding box
          // is the source of truth after layout settled.
          const aside = document.querySelector<HTMLElement>(
            'aside[aria-label="playbook editor chat"]',
          );
          if (aside) onCommit(aside.getBoundingClientRect().height);
        });
      }
    };

    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
  };

  return (
    <div
      onMouseDown={onMouseDown}
      role="separator"
      aria-orientation="horizontal"
      aria-label="resize chat drawer"
      title="drag to resize"
      className="group absolute inset-x-0 -top-1 z-10 h-2 cursor-ns-resize"
    >
      {/* Visible 2px hairline that brightens on hover so the handle
          is discoverable without crowding the drawer chrome. */}
      <div className="mx-auto mt-0.5 h-1 w-16 rounded-full bg-zinc-700 transition group-hover:bg-sky-500" />
    </div>
  );
}

function DrawerHeader({
  subject,
  status,
  onCollapse,
  onTerminate,
}: {
  subject: EditorSubject;
  status: SessionStatus;
  onCollapse: () => void;
  onTerminate: () => void;
}) {
  const primary =
    subject.kind === "playbook" ? subject.playbook.id : subject.wiki.id;
  const secondary =
    subject.kind === "playbook" ? subject.playbook.version : subject.wiki.kind;
  const labelKind = subject.kind === "playbook" ? "playbook editor" : "wiki editor";
  return (
    <header className="flex items-center justify-between gap-3 border-b border-zinc-800 bg-zinc-900/60 px-4 py-2">
      <div className="flex items-baseline gap-2 min-w-0">
        <span className="text-xs uppercase tracking-wide text-zinc-500">
          {labelKind} chat
        </span>
        <span className="truncate font-mono text-sm text-zinc-100">
          {primary}
        </span>
        <span className="font-mono text-xs text-zinc-500">{secondary}</span>
        <StatusPill status={status} />
      </div>
      <div className="flex items-center gap-1">
        <button
          type="button"
          onClick={onCollapse}
          title="collapse (keeps session alive — reopen to resume)"
          aria-label="collapse drawer"
          className="rounded p-1 text-zinc-500 transition hover:bg-zinc-800 hover:text-zinc-200"
        >
          <ChevronDownIcon className="h-4 w-4" />
        </button>
        <button
          type="button"
          onClick={onTerminate}
          title="end session (resets transcript)"
          aria-label="end session"
          className="rounded p-1 text-zinc-500 transition hover:bg-zinc-800 hover:text-red-300"
        >
          <CloseIcon className="h-4 w-4" />
        </button>
      </div>
    </header>
  );
}

function StatusPill({ status }: { status: SessionStatus }) {
  switch (status) {
    case "starting":
      return (
        <span className="inline-flex items-center gap-1 rounded-full bg-zinc-800 px-1.5 py-0.5 text-xs text-zinc-300">
          <Spinner className="h-2.5 w-2.5" /> starting
        </span>
      );
    case "streaming":
      return (
        <span className="inline-flex items-center gap-1 rounded-full bg-sky-900/50 px-1.5 py-0.5 text-xs text-sky-300">
          <Spinner className="h-2.5 w-2.5" /> thinking
        </span>
      );
    case "idle":
      return (
        <span className="rounded-full bg-emerald-900/50 px-1.5 py-0.5 text-xs text-emerald-300">
          idle
        </span>
      );
    case "error":
      return (
        <span className="rounded-full bg-red-900/50 px-1.5 py-0.5 text-xs text-red-300">
          error
        </span>
      );
  }
}

function Transcript({
  items,
  status,
  streamErr,
  dismissedProposalIDs,
  emptyHint,
  onProposalResolved,
}: {
  items: TranscriptItem[];
  status: SessionStatus;
  streamErr: string | null;
  dismissedProposalIDs: ReadonlySet<string> | null;
  emptyHint?: React.ReactNode;
  onProposalResolved?: (
    payload: ProposalDraftPayload,
    kind: "approved" | "declined",
  ) => void;
}) {
  // Stick to bottom on new items unless the operator has scrolled
  // away. Same trick as SessionView.
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const stickToBottom = useRef(true);
  const handleScroll = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    const threshold = 24;
    stickToBottom.current =
      el.scrollHeight - el.clientHeight - el.scrollTop < threshold;
  }, []);
  useEffect(() => {
    if (!stickToBottom.current) return;
    const el = scrollRef.current;
    if (!el) return;
    requestAnimationFrame(() => {
      el.scrollTop = el.scrollHeight;
    });
  }, [items.length]);

  return (
    <div
      ref={scrollRef}
      onScroll={handleScroll}
      className="flex-1 overflow-y-auto rounded border border-zinc-800 bg-zinc-900/30 px-3 py-3"
    >
      {items.length === 0 && (
        <Empty status={status} streamErr={streamErr} hint={emptyHint} />
      )}
      <ul className="space-y-3">
        {items.map((item) => (
          <li key={item.id}>
            <EditorTranscriptItemView
              item={item}
              dismissedProposalIDs={dismissedProposalIDs}
              onProposalResolved={onProposalResolved}
            />
          </li>
        ))}
      </ul>
      {(status === "starting" || status === "streaming") && items.length > 0 && (
        <div className="mt-3 flex items-center gap-2 px-1 text-xs text-zinc-500">
          <Spinner className="h-3 w-3" />
          <span>thinking…</span>
        </div>
      )}
    </div>
  );
}

function Empty({
  status,
  streamErr,
  hint,
}: {
  status: SessionStatus;
  streamErr: string | null;
  hint?: React.ReactNode;
}) {
  if (streamErr) {
    return (
      <div className="rounded border border-red-900/60 bg-red-950/40 p-3 text-xs text-red-200/90">
        {streamErr}
      </div>
    );
  }
  if (status === "starting") {
    return (
      <div className="flex items-center gap-2 text-xs text-zinc-400">
        <Spinner className="h-3 w-3" /> spawning editor agent…
      </div>
    );
  }
  if (hint !== undefined) {
    return <div className="px-1 text-xs text-zinc-500">{hint}</div>;
  }
  return (
    <p className="px-1 text-xs text-zinc-500">
      Ask the assistant to refine this playbook — e.g. <em>"add a branch
      for elasticsearch yellow"</em> or <em>"tighten the symptom wording"</em>.
      It drafts changes, validates them, and proposes a diff for you to
      approve.
    </p>
  );
}

function Composer({
  disabled,
  streaming,
  onSend,
}: {
  disabled: boolean;
  streaming: boolean;
  onSend: (text: string) => Promise<void>;
}) {
  const [text, setText] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function submit(e?: React.FormEvent) {
    e?.preventDefault();
    const t = text.trim();
    if (!t || submitting) return;
    setSubmitting(true);
    setErr(null);
    try {
      await onSend(t);
      setText("");
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : String(e));
    } finally {
      setSubmitting(false);
    }
  }

  function onKey(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      void submit();
    }
  }

  return (
    <form onSubmit={submit} className="space-y-1">
      <div className="relative">
        <textarea
          data-testid="triagent-editor-chat-input"
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={onKey}
          placeholder={
            streaming
              ? "agent is working… you can queue a follow-up"
              : "ask the editor agent — Enter to send, Shift+Enter for newline"
          }
          rows={2}
          disabled={disabled}
          className="w-full resize-y rounded border border-zinc-800 bg-zinc-950 px-3 py-2 pr-11 text-sm text-zinc-100 placeholder-zinc-600 focus:border-zinc-600 focus:outline-none disabled:opacity-60"
        />
        <button
          type="submit"
          data-testid="triagent-editor-chat-send"
          title="send (Enter)"
          aria-label="send"
          disabled={!text.trim() || submitting || disabled || streaming}
          className="absolute right-2 top-1/2 inline-flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded-full bg-zinc-700 text-zinc-200 transition hover:bg-zinc-600 hover:text-zinc-50 disabled:cursor-not-allowed disabled:bg-zinc-800 disabled:text-zinc-600"
        >
          {submitting ? (
            <Spinner className="h-3 w-3" />
          ) : (
            <PlayIcon className="h-3 w-3" />
          )}
        </button>
      </div>
      {err && <div className="text-xs text-red-400">{err}</div>}
    </form>
  );
}

// Editor-specific transcript renderer. Drops the SessionView's
// summarize special-case (the editor agent never summarises) but
// keeps the playbook_proposal_draft → ProposalCard branch so approve/
// decline still works inside the drawer. Memoised so chat-textarea
// keystrokes don't bust ProposalCard's expensive react-diff-viewer
// instance.
const EditorTranscriptItemView = memo(function EditorTranscriptItemView({
  item,
  dismissedProposalIDs,
  onProposalResolved,
}: {
  item: TranscriptItem;
  dismissedProposalIDs: ReadonlySet<string> | null;
  onProposalResolved?: (
    payload: ProposalDraftPayload,
    kind: "approved" | "declined",
  ) => void;
}) {
  switch (item.kind) {
    case "user":
      return (
        <div className="rounded border border-zinc-800 bg-zinc-900/60 px-3 py-2">
          <div className="mb-1 text-xs uppercase tracking-wide text-zinc-500">
            you
          </div>
          <div className="whitespace-pre-wrap text-sm text-zinc-100">
            {item.text}
          </div>
        </div>
      );
    case "assistant":
      return <Markdown text={item.text} />;
    case "tool_call":
      if (item.name === PROPOSE_PLAYBOOK_DRAFT_TOOL_NAME && item.result) {
        try {
          const payload = JSON.parse(item.result) as ProposalDraftPayload;
          if (payload && typeof payload.proposal_id === "string") {
            const dismissedHere =
              dismissedProposalIDs !== null &&
              dismissedProposalIDs.has(payload.proposal_id);
            return (
              <ProposalCard
                payload={payload}
                dismissed={dismissedHere}
                onSendRefinement={async () => {
                  /* The editor drawer doesn't pipe refinements
                     through to the agent — the operator types
                     directly into the composer. Pass a no-op so
                     ProposalCard's "decline + send notes" still
                     decodes the form, but the textarea inside the
                     card is harmless. */
                }}
                onResolved={
                  onProposalResolved
                    ? (kind) => onProposalResolved(payload, kind)
                    : undefined
                }
              />
            );
          }
        } catch {
          /* fall through to the raw tool card */
        }
      }
      return (
        <ToolCard
          toolId={item.toolId}
          name={item.name}
          input={item.input}
          result={item.result}
          pending={item.result === undefined}
          nested={item.children}
        />
      );
    case "error":
      return (
        <div className="rounded border border-red-900/60 bg-red-950/40 px-3 py-2 text-sm text-red-200/90">
          {item.text}
        </div>
      );
  }
});

function PlayIcon({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 16 16"
      fill="currentColor"
      className={className}
      aria-hidden
    >
      <path d="M5 3.5l7 4.5-7 4.5z" />
    </svg>
  );
}

function ChevronDownIcon({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      className={className}
      aria-hidden
    >
      <path d="M3 6l5 5 5-5" />
    </svg>
  );
}

function CloseIcon({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      className={className}
      aria-hidden
    >
      <path d="M3.5 3.5l9 9M12.5 3.5l-9 9" />
    </svg>
  );
}

// useEditorSession owns one editor chat session. Lifecycle:
//
//   - When `enabled` flips true (drawer opened), ensure-or-create the
//     session for the current (playbookID, version) — if a live one
//     exists we resume; otherwise the server creates fresh.
//   - SSE connection is opened immediately after id resolves; closes
//     when drawer collapses or playbook switches.
//   - When (playbookID, version) change while enabled, the prior
//     session is left alone (server-side manager closes it the moment
//     the new session registers). State is reset locally.
//   - terminate() DELETEs the session and clears state. The X button
//     calls this.
//   - Collapsing the drawer keeps the server-side session alive so
//     the operator can resume on reopen.
function useEditorSession({
  subject,
  sources,
  enabled,
}: {
  subject: EditorSubject;
  sources?: EditorSources;
  enabled: boolean;
}) {
  const stream = useStream();
  const [sessionId, setSessionId] = useState<string | null>(null);
  const [items, dispatch] = useReducer(editorReducer, [] as TranscriptItem[]);
  const [status, setStatus] = useState<SessionStatus>("idle");
  const [error, setError] = useState<string | null>(null);

  // Subject identity is the (kind, id, version-or-wiki-kind) tuple.
  // We collapse to a string key so React's effect dep arrays compare
  // cheaply and don't see a fresh object every render.
  const subjectKey =
    subject.kind === "playbook"
      ? `playbook:${subject.playbook.id}@${subject.playbook.version}`
      : `wiki:${subject.wiki.kind}:${subject.wiki.id}`;

  // Ensure / resume the session whenever the (subjectKey, enabled)
  // tuple changes — but only while we don't already own a sessionId.
  // Once a session is established, the subject becomes advisory: the
  // accept-from-__new flow rebinds the session server-side, so a
  // subjectKey change must NOT tear down the live conversation.
  useEffect(() => {
    if (!enabled) return;
    if (sessionId) return; // live session — subject is now advisory
    let cancelled = false;
    setStatus("starting");
    setError(null);
    (async () => {
      try {
        let sess: EditorSession | null = await api.findEditorSession(subject);
        if (!sess) {
          sess = await api.createOrResumeEditorSession(subject, sources);
        }
        if (cancelled) return;
        setSessionId(sess.id);
        setStatus(sess.streaming ? "streaming" : sess.started ? "idle" : "starting");
      } catch (e) {
        if (cancelled) return;
        setError(e instanceof ApiError ? e.message : String(e));
        setStatus("error");
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled, subjectKey, sessionId]);

  // Reset local state when the subject changes — only while we don't
  // already own a sessionId. With a live session, the rebind flow keeps
  // transcript/state intact across subjectKey changes.
  useEffect(() => {
    if (sessionId) return;
    setSessionId(null);
    setStatus("idle");
    setError(null);
    dispatch({ kind: "__reset__" });
  }, [subjectKey, sessionId]);

  // Subscribe to the multiplex stream once we have a session id and the
  // drawer is open. /transcript REST seeds the reducer with the backlog;
  // live envelopes are seq-deduped against it.
  useEffect(() => {
    if (!enabled || !sessionId) return;
    let cancelled = false;
    let lastSeq = 0;
    const buffered: StreamEnvelope[] = [];
    let snapshotApplied = false;

    const unsub = stream.subscribe(
      { scope: "editorSession", id: sessionId },
      (env) => {
        if (cancelled) return;
        if (!snapshotApplied) {
          buffered.push(env);
          return;
        }
        if (env.seq <= lastSeq) return;
        applyEnvelope(env);
      },
    );

    void api
      .getEditorTranscript(sessionId)
      .then((res) => {
        if (cancelled) return;
        lastSeq = res.lastSeq;
        for (const ev of res.events) {
          dispatch(ev);
        }
        // Reconcile status from the seeded backlog. When the drawer
        // attaches after a turn already ended (the create-time turn
        // streams its result/end before SSE subscribes), the live
        // status transition for that terminal event is missed — the
        // backlog carries it but applyEnvelope only runs for live
        // envelopes. Derive idle from a backlog whose last event is
        // terminal so the composer's send button isn't stuck disabled
        // on a phantom "streaming".
        const lastKind = res.events[res.events.length - 1]?.kind;
        if (lastKind === "result" || lastKind === "end") {
          setStatus("idle");
        }
        snapshotApplied = true;
        for (const env of buffered) {
          if (env.seq > lastSeq) applyEnvelope(env);
        }
      })
      .catch(() => {
        /* non-fatal — the live stream still delivers new envelopes */
      });

    function applyEnvelope(env: StreamEnvelope) {
      // Dispatch as EventEnvelope — StreamEnvelope's field set is a
      // strict superset, so the cast is safe.
      dispatch(env as unknown as EventEnvelope);

      if (env.kind === "user") {
        setStatus("streaming");
      } else if (env.kind === "result" || env.kind === "end") {
        setStatus("idle");
      } else if (env.kind === "system" && env.subtype === "init") {
        setStatus("streaming");
      }
    }

    return () => {
      cancelled = true;
      unsub();
    };
  }, [enabled, sessionId, stream]);

  const send = useCallback(
    async (text: string) => {
      if (!sessionId) return;
      await api.sendEditorMessage(sessionId, text);
    },
    [sessionId],
  );

  const terminate = useCallback(async () => {
    if (!sessionId) return;
    try {
      await api.deleteEditorSession(sessionId);
    } catch {
      /* best-effort — server may have already cleaned up on switch */
    }
    setSessionId(null);
    dispatch({ kind: "__reset__" });
    setStatus("idle");
  }, [sessionId]);

  return {
    id: sessionId,
    items,
    status,
    error,
    send,
    terminate,
  };
}
