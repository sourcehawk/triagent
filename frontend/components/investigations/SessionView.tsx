"use client";

import { memo, useCallback, useEffect, useRef, useState } from "react";
import {
  api,
  ApiError,
  resumeAuto,
  restartAuto,
  takeover,
  type Capabilities,
  type Investigation,
  type SessionPushPRRequest,
  type SessionPushPRResult,
} from "@/lib/api";
import { useDialog } from "@/lib/dialog";
import {
  isCreateGithubIssueToolName,
  isDraftPrToolName,
  PROPOSE_PLAYBOOK_DRAFT_TOOL_NAME,
  PROPOSE_WIKI_DRAFT_TOOL_NAME,
  SUMMARIZE_TOOL_NAME,
  type AutoModePayload,
  type CodefixProposalPayload,
  type EventEnvelope,
  type SessionStatus,
  type TranscriptItem,
} from "@/lib/events";
import { Bot } from "lucide-react";
import { useAutoMode } from "@/hooks/useAutoMode";
import { ArchiveButton } from "@/components/sessions/ArchiveButton";
import { PushSessionPRModal } from "@/components/sessions/PushSessionPRModal";
import { CopyButton } from "@/components/shared/CopyButton";
import { Markdown } from "@/components/shared/Markdown";
import { ProposalCard, type ProposalDraftPayload } from "@/components/playbooks/ProposalCard";
import { Spinner } from "@/components/shared/Spinner";
import { ToolCard, toolAnchorId } from "@/components/shared/ToolCard";
import {
  WikiProposalCard,
  type WikiProposalPayload,
} from "@/components/wiki/WikiProposalCard";
import { CodefixProposalCard } from "@/components/codefix/CodefixProposalCard";
import { extractSummarizeResult } from "@/lib/summarize";
import { AutoModeBanner, AutoModeFinishedBanner } from "./SessionView.banners";
import { PlayIcon, HandIcon, StopIcon, Working } from "./SessionView.icons";
import { Header } from "./SessionView.header";
import { PushedBadge, SessionActionButton, UsageReadout } from "./SessionView.status";

// Stable empty-array sentinel for the optional `events` prop. Using a
// module-level constant rather than `[]` inline keeps useAutoMode's
// useMemo dependency stable when the parent hasn't supplied events,
// avoiding a fresh state object on every SessionView render.
const EMPTY_EVENTS: EventEnvelope[] = [];

// PushState is the lifecycle of a single push-to-upstream attempt.
// Lives on SessionView (not the modal) so the modal can close + reopen
// without losing in-flight context, and so the push-to-upstream button
// can disable itself while a push is running.
export type PushState =
  | { phase: "idle" }
  | { phase: "pushing" }
  | { phase: "success"; result: SessionPushPRResult }
  | { phase: "error"; message: string };

// Canned messages sent on session-action button clicks. Different
// wording per mode so the agent can route into the right branch of the
// corresponding meta-playbook (new vs edit / first-pass vs follow-up).
const PROMOTE_TO_WIKI_PROMPT =
  "Please write this investigation up as a wiki entry now. Use the propose_wiki_draft tool.";
const EDIT_WIKI_PROMPT =
  "Please iterate on the existing wiki entry for this investigation — call propose_wiki_draft again with the same slug so the tool loads the existing entry as base_md and the sub-agent revises rather than drafts from scratch.";
const PROPOSE_PLAYBOOK_PROMPT =
  "Please walk the playbook_proposal meta-playbook now and, if the bar is met, submit a playbook_proposal_draft for review.";
const EDIT_PLAYBOOK_PROMPT =
  "Please revisit the playbook proposal for this investigation — re-walk playbook_proposal with what we've learned since the last draft and submit an updated playbook_proposal_draft for review.";
const REQUEST_CODEFIX_PROMPT =
  "Please walk the pr_proposal meta-playbook now and, if the bar is met, draft an issue + PR for the affected linked repo(s).";
const EDIT_CODEFIX_PROMPT =
  "Please address the review comments on the open codefix PR — re-walk pr_proposal in edit mode, fetch the open PR's review comments, and force-push refinements to the same branch.";
// bug-report sibling of codefix: walks bug_report_proposal which stops
// at create_github_issue (no draft_pr). Used when the change is too
// large/cross-team/contentious for a single sub-agent run, or when the
// operator wants to triage rather than fix. No edit-mode variant —
// re-clicking files a fresh issue, which is the expected next-iteration
// behaviour for bug reports.
const REPORT_BUG_PROMPT =
  "Please walk the bug_report_proposal meta-playbook now and, if the bar is met, file an issue (no draft PR) against the affected linked repo(s).";

type Props = {
  investigation: Investigation;
  items: TranscriptItem[];
  // events is the raw event stream the parent has accumulated. We use
  // it (rather than `items`) for auto-mode UI gating because the
  // useAutoMode hook folds auto_mode_state envelopes that the
  // transcript reducer deliberately ignores — they don't render as
  // chat items, only as UI state. Optional so existing call sites that
  // haven't been updated yet (and tests for non-auto-mode behaviour)
  // can omit it; the hook handles an empty list as "no auto mode".
  events?: EventEnvelope[];
  status: SessionStatus;
  streamErr: string | null;
  capabilities: Capabilities | null;
  onUpdated?: (next: Investigation) => void;
  // Latest server-emitted push_state event, set by the parent's SSE
  // listener. SessionView uses it to drive toast transitions and to
  // clear the local PushState when the corresponding event arrives.
  // Named `serverPushState` to avoid colliding with the existing local
  // `pushState` state variable.
  serverPushState?: { phase: "pushing" | "success" | "error"; url?: string; branch?: string; base?: string; error?: string };
  onServerPushStateConsumed?: () => void;
  // Latest server-emitted rehydrate_state event. Drives the resumable
  // banner through started / succeeded / failed phases.
  serverRehydrateState?: { phase: "started" | "succeeded" | "failed"; error?: string; degraded?: string[] };
  onServerRehydrateStateConsumed?: () => void;
};

// SessionView renders the chat: header, scrollable transcript, follow-up
// textarea. Transcript state and the SSE connection live in the parent
// SessionWorkspace so the activity panel can share them.
export function SessionView({
  investigation,
  items,
  events,
  status,
  streamErr,
  capabilities,
  onUpdated,
  serverPushState,
  onServerPushStateConsumed,
  serverRehydrateState,
  onServerRehydrateStateConsumed,
}: Props) {
  // Auto-mode UI state derived from the raw event stream. When the
  // parent doesn't pass events (older call sites, narrowly-scoped
  // tests), useAutoMode returns the fully-off shape so every gate
  // below resolves to "no auto mode" — i.e. the composer and Send
  // button behave exactly as they did pre-T23.
  const auto = useAutoMode(events ?? EMPTY_EVENTS);
  const [followUp, setFollowUp] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [submitErr, setSubmitErr] = useState<string | null>(null);
  // pendingUserMessage holds the text of an in-flight follow-up so it
  // can render in the transcript immediately rather than waiting on
  // the round trip (which, on a resumed session, also blocks on
  // rehydrate). Cleared once the fetch resolves; rendered only if the
  // server-sent user envelope hasn't already landed in items.
  const [pendingUserMessage, setPendingUserMessage] = useState<string | null>(
    null,
  );
  const [pushOpen, setPushOpen] = useState(false);
  // Push state lives at the SessionView level so it survives modal
  // close + reopen, and the push-to-upstream button can disable
  // itself while a push is in flight.
  const [pushState, setPushState] = useState<PushState>({ phase: "idle" });
  // Rehydrate state drives the resumable banner through its phases.
  const [rehydratePhase, setRehydratePhase] = useState<"started" | "succeeded" | "failed" | null>(null);
  const [rehydrateError, setRehydrateError] = useState<string | null>(null);
  const dialog = useDialog();

  // Reset push state when the active investigation changes (the user
  // navigated to a different session).
  useEffect(() => {
    setPushState({ phase: "idle" });
    pushToastIdRef.current = null;
  }, [investigation.id]);

  // Toast id of the currently-pending push toast, if any. Lives in a
  // ref so the SSE-driven update path can find it without re-running
  // startPush. Stays set across re-renders for the same push.
  const pushToastIdRef = useRef<string | null>(null);

  const showPendingToast = useCallback(() => {
    if (pushToastIdRef.current) return pushToastIdRef.current;
    const id = dialog.notify({
      kind: "pending",
      title: "Drafting session for PR",
      body: "An AI sub-agent is summarizing the transcript. This usually takes 30s–3m.",
    });
    pushToastIdRef.current = id;
    return id;
  }, [dialog]);

  // If the server reports an in-progress push (e.g. we just refreshed
  // mid-push), restore the pending toast and the local pushing state
  // so the button stays disabled until the SSE result arrives.
  useEffect(() => {
    if (investigation.pushInProgress) {
      setPushState({ phase: "pushing" });
      showPendingToast();
    } else if (investigation.pushError && pushState.phase === "idle") {
      setPushState({ phase: "error", message: investigation.pushError });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [investigation.pushInProgress, investigation.pushError, showPendingToast]);

  // SSE-driven push state transitions. When the parent's SSE listener
  // delivers a push_state envelope, this effect updates the local
  // pushState and the pending toast accordingly.
  useEffect(() => {
    if (!serverPushState) return;
    if (serverPushState.phase === "pushing") {
      showPendingToast();
      onServerPushStateConsumed?.();
      return;
    }
    if (serverPushState.phase === "success") {
      setPushState({
        phase: "success",
        result: {
          url: serverPushState.url ?? "",
          branch: serverPushState.branch ?? "",
          base: serverPushState.base ?? "",
          slug: investigation.slug ?? "",
        },
      });
      if (pushToastIdRef.current) {
        dialog.update(pushToastIdRef.current, {
          kind: "success",
          title: "Session pushed upstream",
          body: `Branch ${serverPushState.branch} → ${serverPushState.base}.`,
          action: serverPushState.url ? { label: "open PR", href: serverPushState.url } : undefined,
          persistent: true,
        });
        pushToastIdRef.current = null;
      }
      onServerPushStateConsumed?.();
      return;
    }
    if (serverPushState.phase === "error") {
      setPushState({ phase: "error", message: serverPushState.error ?? "Push failed" });
      if (pushToastIdRef.current) {
        dialog.update(pushToastIdRef.current, {
          kind: "error",
          title: "Push failed",
          body: serverPushState.error ?? "Push failed",
          persistent: true,
        });
        pushToastIdRef.current = null;
      }
      onServerPushStateConsumed?.();
    }
  }, [serverPushState, dialog, investigation.slug, showPendingToast, onServerPushStateConsumed]);

  // SSE-driven rehydrate state transitions. Drives the resumable banner
  // through started / succeeded / failed without blocking the composer.
  useEffect(() => {
    if (!serverRehydrateState) return;
    setRehydratePhase(serverRehydrateState.phase);
    setRehydrateError(serverRehydrateState.error ?? null);
    onServerRehydrateStateConsumed?.();
  }, [serverRehydrateState, onServerRehydrateStateConsumed]);

  const startPush = useCallback(
    async (req: SessionPushPRRequest) => {
      setPushState({ phase: "pushing" });
      showPendingToast();
      try {
        const res = await api.pushSessionPR(investigation.id, req);
        // 202 path: server accepted; result will arrive via SSE.
        // Validation-error path: surface immediately because the
        // server returns 400 before the goroutine even spawns.
        if (!res.ok) {
          setPushState({ phase: "error", message: res.errors.join(" · ") });
          if (pushToastIdRef.current) {
            dialog.update(pushToastIdRef.current, {
              kind: "error",
              title: "Push failed (validation)",
              body: res.errors.join(" · "),
              persistent: true,
            });
            pushToastIdRef.current = null;
          }
        }
      } catch (e) {
        const msg = e instanceof ApiError ? e.message : String(e);
        setPushState({ phase: "error", message: msg });
        if (pushToastIdRef.current) {
          dialog.update(pushToastIdRef.current, {
            kind: "error",
            title: "Push failed",
            body: msg,
            persistent: true,
          });
          pushToastIdRef.current = null;
        }
      }
    },
    [dialog, investigation.id, showPendingToast],
  );

  const closePushModal = useCallback(() => {
    setPushOpen(false);
    // After error, drop back to idle so reopening the modal shows
    // a fresh form. After success, keep the success state — the
    // archived banner now shows "PR open ↗" using investigation.pushUrl
    // anyway, and the toast is sticky.
    setPushState((prev) => (prev.phase === "error" ? { phase: "idle" } : prev));
  }, []);

  // Stable callback for the proposal-card refinement send. Inline
  // closures here would create a fresh function identity on every
  // keystroke in the chat textarea, busting React.memo on
  // TranscriptItemView and forcing the (expensive) diff viewer to
  // re-render — the typing-lag culprit.
  const sendRefinement = useCallback(
    (text: string) => api.sendMessage(investigation.id, text),
    [investigation.id],
  );

  // Track whether the user has scrolled away from the bottom. When at
  // bottom, new items autoscroll; when scrolled up, we leave them be so
  // they don't lose their place mid-read.
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

  async function submitFollowUp(e?: React.FormEvent) {
    e?.preventDefault();
    const text = followUp.trim();
    if (!text || submitting) return;
    setSubmitting(true);
    setSubmitErr(null);
    // Clear + lock the composer up front and optimistically render
    // the user's message in the transcript. On a resumed session the
    // round trip blocks on rehydrate (several seconds); leaving the
    // text in the textarea while we wait reads as "nothing happened".
    setFollowUp("");
    setPendingUserMessage(text);
    stickToBottom.current = true;
    try {
      await api.sendMessage(investigation.id, text);
    } catch (e) {
      setSubmitErr(e instanceof ApiError ? e.message : String(e));
      // Restore the text so the operator can edit + retry. The
      // pending placeholder disappears with submitting below.
      setFollowUp(text);
    } finally {
      setSubmitting(false);
      setPendingUserMessage(null);
    }
  }

  // Stop the in-flight claude turn. Mirrors ChatGPT's send/stop swap —
  // while streaming, the composer's chip flips from a play wedge to a
  // square; clicking it cancels the per-turn context server-side. The
  // end-of-turn envelope flips streaming back to false (handled by the
  // existing stream subscription), which restores the Send chip
  // automatically. 409s are swallowed: they mean the turn already
  // finished in the gap between click and request, which is fine.
  async function interruptTurn() {
    try {
      await api.interruptInvestigation(investigation.id);
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) return;
      setSubmitErr(e instanceof ApiError ? e.message : String(e));
    }
  }

  function onTextareaKey(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    // Enter sends; Shift+Enter inserts a newline. Modifier-Enter
    // (Cmd/Ctrl/Alt) also sends, so muscle memory from the previous
    // shortcut still works.
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      void submitFollowUp();
    }
  }

  // Eligibility for the session-action buttons + the header's
  // "view latest summary" jump. The summary gate is shared: both the
  // wiki entry and a playbook proposal are best produced once the
  // agent has crystallised a formal summary, so we refuse the
  // session-action buttons until at least one summarize tool call has
  // landed in the transcript. We also remember the *latest* summary's
  // toolId so the header can scroll to it on demand.
  const latestSummaryToolId = (() => {
    for (let i = items.length - 1; i >= 0; i--) {
      const it = items[i];
      if (
        it.kind === "tool_call" &&
        it.name === SUMMARIZE_TOOL_NAME &&
        typeof it.result === "string" &&
        it.result.length > 0
      ) {
        return it.toolId;
      }
    }
    return null;
  })();
  const hasSummary = latestSummaryToolId !== null;

  // Has the agent already proposed a wiki / playbook draft this
  // session? If yes, the buttons flip into "edit" mode — clicking
  // sends a different canned message that asks the agent to iterate
  // on the existing draft rather than start from scratch. The
  // existence of a tool result (regardless of whether the operator
  // approved it) is what flips the mode: a declined draft is still
  // "we've been around this loop once", so the next prompt should be
  // the iterate variant.
  const hasWikiProposal = items.some(
    (it) =>
      it.kind === "tool_call" &&
      it.name === PROPOSE_WIKI_DRAFT_TOOL_NAME &&
      typeof it.result === "string" &&
      it.result.length > 0,
  );
  const hasPlaybookProposal = items.some(
    (it) =>
      it.kind === "tool_call" &&
      it.name === PROPOSE_PLAYBOOK_DRAFT_TOOL_NAME &&
      typeof it.result === "string" &&
      it.result.length > 0,
  );
  const hasCodefixProposal = items.some(
    (it) =>
      it.kind === "tool_call" &&
      isDraftPrToolName(it.name) &&
      typeof it.result === "string" &&
      it.result.length > 0,
  );

  const wikiOK = capabilities?.wiki?.valid ?? false;
  const wikiEnabled = hasSummary && wikiOK;
  const wikiDisabledReason = !hasSummary
    ? "At least one summary must be generated before a wiki entry can be created"
    : !wikiOK
      ? `wiki vault not ready: ${capabilities?.wiki?.reason ?? "set --wiki-path / --wiki-repo at launcher"}`
      : "";

  const playbookEnabled = hasSummary;
  const playbookDisabledReason = !hasSummary
    ? "At least one summary must be generated before a playbook proposal can be drafted"
    : "";

  const triggerWiki = () => {
    void api.sendMessage(
      investigation.id,
      hasWikiProposal ? EDIT_WIKI_PROMPT : PROMOTE_TO_WIKI_PROMPT,
    );
  };
  const triggerPlaybook = () => {
    void api.sendMessage(
      investigation.id,
      hasPlaybookProposal ? EDIT_PLAYBOOK_PROMPT : PROPOSE_PLAYBOOK_PROMPT,
    );
  };

  const codefixCap = capabilities?.codefix;
  const codefixEnabled = hasSummary && (codefixCap?.enabled ?? false);
  const codefixDisabledReason = !hasSummary
    ? "At least one summary must be generated before a codefix can be drafted"
    : codefixCap?.reason ?? "Codefix capability unavailable";

  const triggerCodefix = () => {
    void api.sendMessage(
      investigation.id,
      hasCodefixProposal ? EDIT_CODEFIX_PROMPT : REQUEST_CODEFIX_PROMPT,
    );
  };

  // bug-report shares the codefix capability gate — both need gh auth
  // AND at least one linked repo. No "edit" mode: filing the same
  // investigation as a fresh issue twice is rare and intentional, so
  // each click walks request mode.
  const bugReportEnabled = codefixEnabled;
  const bugReportDisabledReason = !hasSummary
    ? "At least one summary must be generated before a bug report can be filed"
    : codefixCap?.reason ?? "Bug report capability unavailable";
  const triggerBugReport = () => {
    void api.sendMessage(investigation.id, REPORT_BUG_PROMPT);
  };

  return (
    // h-full + flex-col so the section fills whatever vertical space
    // the parent gives it (TopNav above + py-6 wrapper around). A
    // hardcoded 100dvh-N here would drift the moment the surrounding
    // chrome changes; let the layout chain do the maths.
    <section className="flex h-full flex-col gap-3">
      <Header
        investigation={investigation}
        status={status}
        latestSummaryToolId={latestSummaryToolId}
      />

      <div
        ref={scrollRef}
        onScroll={handleScroll}
        className="flex-1 overflow-y-auto rounded border border-zinc-800 bg-zinc-900/30 px-4 py-4"
      >
        {/* Prominent auto-mode cue. The whole banner is a clickable
            takeover affordance; the small chip in the composer slot
            stays as a fallback for when the banner scrolls offscreen. */}
        {auto.canTakeOver && (
          <AutoModeBanner investigationId={investigation.id} />
        )}
        {(auto.phase === "finished" || auto.phase === "aborted") && (
          <AutoModeFinishedBanner
            investigationId={investigation.id}
            phase={auto.phase}
            reason={auto.lastReason}
          />
        )}
        {items.length === 0 && (
          <Empty status={status} streamErr={streamErr} />
        )}
        <ul className="space-y-3" data-testid="triagent-transcript-list">
          {items.map((item) => (
            <li key={item.id}>
              <TranscriptItemView
                item={item}
                capabilities={capabilities}
                onSendRefinement={sendRefinement}
                streaming={status === "streaming"}
              />
            </li>
          ))}
          {/* Optimistic placeholder for an in-flight follow-up. Hidden
              once the server-sent user envelope has landed in items
              (matched by text on the tail) so the two don't overlap. */}
          {pendingUserMessage &&
            (() => {
              const tail = items[items.length - 1];
              if (tail && tail.kind === "user" && tail.text === pendingUserMessage) {
                return null;
              }
              return (
                <li>
                  <PendingUserMessage text={pendingUserMessage} />
                </li>
              );
            })()}
        </ul>
        {/* Pinned indicator while claude is mid-turn. Sits below the
            last item, disappears the moment a result/end event flips
            status back to idle. */}
        {(status === "starting" || status === "streaming") && items.length > 0 && (
          <Working />
        )}
      </div>

      {investigation.archived ? (
        <div className="space-y-2 rounded border border-zinc-800 bg-zinc-900/40 p-3 text-sm text-zinc-400">
          <p>
            This investigation is archived. The transcript is restored from disk; to ask
            a follow-up, start a new investigation against the same cluster.
          </p>
          <div className="flex items-center gap-2">
            {/* "push to upstream" appears when the resolver says
                the session isn't synced — covers both "never pushed"
                (local-only) and "previous PR closed without merging"
                (closed). Goes through SyncState rather than rolling
                its own (!pushed || prState === "closed") formula so
                this gate stays in lockstep with the sidebar checkmark
                and the ImportedFromBadge gate further down.
                Also shown after a failed prior attempt (phase === "error")
                so an orphaned push from a server restart is retryable. */}
            {investigation.syncState.status !== "synced" &&
              (pushState.phase === "idle" || pushState.phase === "error") &&
              !investigation.pushInProgress &&
              // Hide the push button only when capabilities have loaded
              // AND there's no upstream sessions repo to push to. While
              // capabilities are still null/loading, leave the button
              // visible so the layout doesn't flicker the moment the
              // fetch completes for a configured operator.
              (capabilities === null ||
                (capabilities.sessions?.repo ?? "").length > 0) && (
                <button
                  type="button"
                  onClick={() => setPushOpen(true)}
                  className="rounded border border-zinc-700 px-2 py-1 text-xs text-zinc-200 transition hover:border-zinc-500"
                  title={
                    pushState.phase === "error"
                      ? `previous push failed: ${pushState.message} — click to retry`
                      : investigation.syncState.status === "closed"
                        ? "the previous PR was closed without merging — push again to open a new one"
                        : "push this session to the upstream sessions repo"
                  }
                >
                  {pushState.phase === "error" ? "retry push to upstream" : "push to upstream"}
                </button>
              )}
            {(pushState.phase === "pushing" || investigation.pushInProgress) && (
              <button
                type="button"
                onClick={() => setPushOpen(true)}
                className="inline-flex items-center gap-1.5 rounded border border-zinc-700 px-2 py-1 text-xs text-zinc-300"
                title="a push is already in progress — click to see status"
              >
                <Spinner className="h-3 w-3" />
                pushing…
              </button>
            )}
            {investigation.pushed && pushState.phase !== "pushing" && (
              <PushedBadge
                pushUrl={investigation.pushUrl}
                prState={investigation.prState}
                prMergedAt={investigation.prMergedAt}
                prClosedAt={investigation.prClosedAt}
              />
            )}
          </div>
          {pushState.phase === "error" && !investigation.pushInProgress && (
            <p className="text-xs text-red-400/90">
              Previous push failed: {pushState.message}
            </p>
          )}
        </div>
      ) : (
        <div className="space-y-1.5">
          {/* Resumable banner: shown when a restored session is waiting
              for a follow-up to trigger rehydration, and disappears once
              the rehydrate orchestration is in flight. */}
          {investigation.resumable && rehydratePhase !== "started" && (
            <div className="rounded border border-amber-800 bg-amber-900/20 p-3 text-sm text-amber-200">
              <p>Resume by sending a message — the agent will reconnect to the cluster on your next prompt.</p>
              {rehydratePhase === "failed" && rehydrateError && (
                <p className="mt-1 text-amber-300">Last rehydrate attempt failed: {rehydrateError}. Retry by resending, or click Archive to finalise.</p>
              )}
            </div>
          )}
          {/* Rehydrate-in-progress banner: transient spinner copy while
              the server reconnects to the cluster. */}
          {rehydratePhase === "started" && (
            <div className="rounded border border-amber-800 bg-amber-900/20 p-3 text-sm text-amber-200">
              Reconnecting to cluster…
            </div>
          )}
          {/* Session-level action row: wiki + playbook + codefix +
              bug-report triggers. Rendered above the chat textarea so
              they don't crowd the send button. All buttons gate on a
              summary having landed in the transcript. The first three
              flip into "edit" mode once their corresponding draft
              tool has been called at least once; the bug-report
              button stays single-mode because re-clicking files a
              fresh issue (which is the expected next-iteration
              behaviour for bug reports). */}
          <div className="flex items-center gap-2">
            {onUpdated && (
              <ArchiveButton investigation={investigation} onArchived={onUpdated} />
            )}
            <SessionActionButton
              tone="emerald"
              enabled={wikiEnabled}
              disabledReason={wikiDisabledReason}
              enabledTitle={
                hasWikiProposal
                  ? "Ask the agent to iterate on the existing wiki entry"
                  : "Ask the agent to draft a wiki entry from this investigation"
              }
              onClick={triggerWiki}
              label={hasWikiProposal ? "edit wiki" : "promote to wiki"}
            />
            <SessionActionButton
              tone="sky"
              enabled={playbookEnabled}
              disabledReason={playbookDisabledReason}
              enabledTitle={
                hasPlaybookProposal
                  ? "Ask the agent to revisit the playbook proposal"
                  : "Ask the agent to walk the playbook_proposal meta-playbook"
              }
              onClick={triggerPlaybook}
              label={
                hasPlaybookProposal ? "edit playbook" : "propose playbook"
              }
            />
            <SessionActionButton
              tone="amber"
              enabled={codefixEnabled}
              disabledReason={codefixDisabledReason}
              enabledTitle={
                hasCodefixProposal
                  ? "Ask the agent to address the open codefix PR's review comments"
                  : "Ask the agent to walk the pr_proposal meta-playbook"
              }
              onClick={triggerCodefix}
              label={hasCodefixProposal ? "edit codefix" : "request codefix"}
            />
            <SessionActionButton
              tone="rose"
              enabled={bugReportEnabled}
              disabledReason={bugReportDisabledReason}
              enabledTitle="Ask the agent to walk the bug_report_proposal meta-playbook (files an issue, no draft PR)"
              onClick={triggerBugReport}
              label="report bug"
            />
            <UsageReadout
              usage={investigation.usage}
              costUsd={investigation.costUsd}
            />
          </div>
        <form onSubmit={submitFollowUp} className="space-y-1">
          {/* relative wrapper so the send button can overlay the
              textarea's bottom-right corner. Right padding on the
              textarea reserves space so long lines don't slide under
              the icon. */}
          <div className="relative">
            <textarea
              data-testid="triagent-composer-input"
              value={followUp}
              onChange={(e) => setFollowUp(e.target.value)}
              onKeyDown={onTextareaKey}
              placeholder={
                auto.canTakeOver
                  ? "Auto mode is active."
                  : status === "streaming"
                    ? "claude is working — draft your follow-up, then hit ◼ to stop the agent and send it"
                    : "ask a follow-up — Enter to send, Shift+Enter for newline"
              }
              rows={3}
              // When auto mode is driving the session, the composer is
              // off-limits — the operator goes through Take over first
              // so the launcher can pause the AutoOperator before any
              // human prompt lands. Once paused/finished/aborted the
              // textarea unlocks again and the action-button slot
              // surfaces Resume / Restart instead of plain Send.
              disabled={status === "starting" || submitting || auto.canTakeOver}
              className="w-full resize-y rounded border border-zinc-800 bg-zinc-950 px-3 py-2 pr-11 text-sm text-zinc-100 placeholder-zinc-600 focus:border-zinc-600 focus:outline-none disabled:opacity-60"
            />
            {/* Action-button slot — the round chip on the textarea's
                right edge. Auto-mode gates take precedence over the
                normal Send affordance:
                  • started/resumed → Take over (pauses the operator)
                  • paused          → Resume auto mode
                  • finished/aborted → Restart auto mode
                Each branch renders an icon-only chip so it stays in
                the same visual slot as Send; aria-label + title carry
                the verb for accessibility + tests. */}
            {auto.canTakeOver ? (
              <button
                type="button"
                title="Take over (pauses the auto operator)"
                aria-label="Take over"
                onClick={() => {
                  void takeover(investigation.id);
                }}
                className="absolute right-2 top-1/2 inline-flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded-full bg-pink-600 text-pink-50 transition hover:bg-pink-500"
              >
                <HandIcon className="h-3 w-3" />
              </button>
            ) : auto.canResume ? (
              <button
                type="button"
                title="Resume auto mode"
                aria-label="Resume auto mode"
                onClick={() => {
                  void resumeAuto(investigation.id);
                }}
                className="absolute right-2 top-1/2 inline-flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded-full bg-pink-600 text-pink-50 transition hover:bg-pink-500"
              >
                <PlayIcon className="h-3 w-3" />
              </button>
            ) : auto.canRestart ? (
              <button
                type="button"
                title="Restart auto mode"
                aria-label="Restart auto mode"
                onClick={() => {
                  void restartAuto(investigation.id);
                }}
                className="absolute right-2 top-1/2 inline-flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded-full bg-pink-600 text-pink-50 transition hover:bg-pink-500"
              >
                <PlayIcon className="h-3 w-3" />
              </button>
            ) : status === "streaming" ? (
              // ChatGPT-style send↔stop swap. While streaming, the
              // chip becomes Stop and interrupts the in-flight turn.
              // When the agent's `end` envelope lands, status flips
              // back to "idle" and the Send chip returns automatically.
              <button
                type="button"
                title="stop the agent's current turn"
                aria-label="stop"
                onClick={() => {
                  void interruptTurn();
                }}
                className="absolute right-2 top-1/2 inline-flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded-full bg-zinc-700 text-zinc-200 transition hover:bg-zinc-600 hover:text-zinc-50"
              >
                <StopIcon className="h-3 w-3" />
              </button>
            ) : (
              <button
                type="submit"
                data-testid="triagent-composer-send"
                title="send (Enter)"
                aria-label="send"
                disabled={
                  !followUp.trim() ||
                  submitting ||
                  status === "starting"
                }
                // Vertically centered on the textarea's right edge via
                // top-1/2 + -translate-y-1/2. Light-grey play-icon
                // styling: a circular muted-zinc chip that brightens on
                // hover.
                className="absolute right-2 top-1/2 inline-flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded-full bg-zinc-700 text-zinc-200 transition hover:bg-zinc-600 hover:text-zinc-50 disabled:cursor-not-allowed disabled:bg-zinc-800 disabled:text-zinc-600"
              >
                {submitting ? (
                  <Spinner className="h-3 w-3" />
                ) : (
                  <PlayIcon className="h-3 w-3" />
                )}
              </button>
            )}
          </div>
          {submitErr && (
            <div className="text-xs text-red-400">{submitErr}</div>
          )}
        </form>
        </div>
      )}
      {pushOpen && (
        <PushSessionPRModal
          investigation={investigation}
          state={pushState}
          onSubmit={startPush}
          onClose={closePushModal}
        />
      )}
    </section>
  );
}

// StateDivider renders an auto_mode_state phase transition as an
// inline divider chip — a horizontal pink rule with a centered
// "— auto mode <phase> —" pill. Folded out of auto_mode_state
// envelopes by applyEvent (lib/events.ts) so it interleaves with
// chat turns in seq order. takenOverBy annotates pause-by-human
// vs pause-by-operator (request_takeover); reason is shown in the
// title attribute so it doesn't fight for vertical space.
function StateDivider({ payload }: { payload: AutoModePayload }) {
  const label =
    `auto mode ${payload.phase}` +
    (payload.takenOverBy ? ` (${payload.takenOverBy})` : "");
  return (
    <div
      className="my-2 flex items-center gap-2 text-xs text-zinc-500"
      title={payload.reason ?? payload.error ?? undefined}
      data-testid="auto-mode-divider"
      data-phase={payload.phase}
    >
      <span className="flex-1 border-t border-pink-500/40" />
      <span className="rounded-full border border-pink-500/40 px-2 py-0.5">
        — {label} —
      </span>
      <span className="flex-1 border-t border-pink-500/40" />
    </div>
  );
}

function Empty({
  status,
  streamErr,
}: {
  status: SessionStatus;
  streamErr: string | null;
}) {
  if (streamErr) {
    return (
      <div className="rounded border border-red-900/60 bg-red-950/40 p-3 text-sm text-red-200/90">
        {streamErr}
      </div>
    );
  }
  if (status === "starting") {
    return (
      <div className="flex items-center gap-2 text-sm text-zinc-400">
        <Spinner /> spawning claude — this takes a few seconds the first
        time…
      </div>
    );
  }
  return null;
}

// PendingUserMessage renders the operator's just-submitted prompt in
// the transcript while the POST is in flight. Mirrors the kind: "user"
// branch of TranscriptItemView so the optimistic placeholder is
// indistinguishable from the real item, modulo a soft opacity + an
// inline spinner labelled "sending…".
function PendingUserMessage({ text }: { text: string }) {
  return (
    <div className="rounded border border-zinc-700 bg-zinc-800/70 px-3 py-2 opacity-80">
      <div className="mb-1 flex items-center gap-2 text-xs uppercase tracking-wide text-zinc-300">
        <span>you</span>
        <Spinner className="h-3 w-3" />
        <span className="normal-case text-zinc-400">sending…</span>
      </div>
      <div className="whitespace-pre-wrap text-sm text-zinc-50">{text}</div>
    </div>
  );
}

// FALLBACK_CAPABILITIES is used when the capabilities fetch hasn't
// completed yet (null) — both gh and wiki show as unavailable so the
// WikiProposalCard's approve button is safely disabled while loading.
const FALLBACK_CAPABILITIES: Capabilities = {
  gh: { installed: false, authenticated: false },
  repoPath: { configured: false, valid: false },
  wiki: { configured: false, valid: false },
  claude: { installed: false },
  sessions: { configured: false, valid: false },
};

// Memoised so chat-textarea keystrokes (which bump SessionView
// state on every input event) don't force every transcript row to
// re-render. The streaming reducer mutates only the latest tool_call
// while a tool is in flight; everything older keeps a stable
// reference and skips the work entirely. Critical for performance
// because the playbook_proposal_draft branch renders a full
// react-diff-viewer instance — that's expensive enough to be
// perceptible on every keystroke.
const TranscriptItemView = memo(function TranscriptItemView({
  item,
  capabilities,
  onSendRefinement,
  streaming,
}: {
  item: TranscriptItem;
  capabilities: Capabilities | null;
  onSendRefinement: (text: string) => Promise<void> | void;
  streaming: boolean;
}) {
  switch (item.kind) {
    case "user": {
      // Operator-authored user turns (auto-mode send_message tool) get
      // pink-accented chrome + a Bot glyph so the operator can tell at a
      // glance which prompts came from a human vs. the AutoOperator.
      // Falls back to the original zinc styling for human turns and for
      // legacy envelopes that pre-date the Origin field.
      const isOperator = item.origin === "operator";
      return (
        <div
          data-testid="user-envelope"
          data-origin={item.origin ?? "human"}
          className={
            isOperator
              ? "rounded border border-pink-500/50 bg-pink-500/10 px-3 py-2"
              : "rounded border border-zinc-700 bg-zinc-800/70 px-3 py-2"
          }
        >
          <div
            className={
              "mb-1 flex items-center gap-1.5 text-xs uppercase tracking-wide " +
              (isOperator ? "font-medium text-pink-300" : "text-zinc-300")
            }
          >
            {isOperator && (
              <Bot
                aria-label="auto operator"
                className="h-3.5 w-3.5 text-pink-300"
              />
            )}
            <span>{isOperator ? "Auto Operator" : "you"}</span>
          </div>
          <div className="whitespace-pre-wrap text-sm text-zinc-50">
            {item.text}
          </div>
        </div>
      );
    }
    case "auto_mode_divider":
      return <StateDivider payload={item.payload} />;
    case "assistant":
      return (
        <div data-testid="triagent-assistant-message">
          <Markdown text={item.text} />
        </div>
      );
    case "tool_call":
      // Auto-operator's own MCP tool calls (send_message / finish /
      // request_takeover) are hidden from the main transcript — they
      // appear in the ActivityPanel's Auto-operator group instead. The
      // user-facing result of send_message is the synthetic operator
      // user envelope above; the rest are internal to auto mode and
      // would just duplicate as blue tool cards inline if we let them
      // render here.
      if (item.name.startsWith("mcp__triagent-agent-operator__")) {
        return null;
      }
      // The strategies playbook_proposal_draft tool's result is the
      // diff payload — render it as a ProposalCard (side-by-side diff
      // + Approve/Decline) instead of a plain tool card. The card
      // posts to /api/playbook-proposals/<id>/approve or /decline on
      // the operator's click; the agent isn't in the loop for the
      // approve/decline event itself.
      if (
        item.name === PROPOSE_PLAYBOOK_DRAFT_TOOL_NAME &&
        item.result
      ) {
        try {
          const payload = JSON.parse(item.result) as ProposalDraftPayload;
          // A non-empty proposal_id means the draft was accepted and
          // persisted. A validation-failure result carries proposal_id:""
          // (plus validation_errors); it must NOT render as a proposal card —
          // fall through to the raw tool card so the failed attempt is visible
          // as activity, not an empty approve/decline card.
          if (payload && typeof payload.proposal_id === "string" && payload.proposal_id.length > 0) {
            return (
              <div data-testid="triagent-proposal-card" data-proposal-kind="playbook">
                <ProposalCard
                  payload={payload}
                  onSendRefinement={onSendRefinement}
                />
              </div>
            );
          }
        } catch {
          /* fall through to the raw tool card if the JSON is unparseable */
        }
      }
      // The wiki propose_wiki_draft tool's result is the draft payload —
      // render it as a WikiProposalCard. The card posts to
      // /api/wiki-proposals/<id>/approve or /decline. Approve opens a PR;
      // decline drops the local draft and (optionally) sends refinement
      // notes back to the agent.
      if (item.name === PROPOSE_WIKI_DRAFT_TOOL_NAME && item.result) {
        try {
          const payload = JSON.parse(item.result) as WikiProposalPayload;
          // A non-empty proposal_id means the draft was accepted and
          // persisted. A validation-failure result carries proposal_id:""
          // (plus validation_errors); it must NOT render as a proposal card —
          // fall through to the raw tool card so the failed attempt is visible
          // as activity, not an empty approve/decline card.
          if (payload && typeof payload.proposal_id === "string" && payload.proposal_id.length > 0) {
            return (
              <div data-testid="triagent-proposal-card" data-proposal-kind="wiki">
                <WikiProposalCard
                  payload={payload}
                  capabilities={capabilities ?? FALLBACK_CAPABILITIES}
                  onSendRefinement={onSendRefinement}
                  streaming={streaming}
                />
              </div>
            );
          }
        } catch {
          /* fall through to the raw tool card if the JSON is unparseable */
        }
      }
      // The triagent-git-<alias>/draft_pr tool's result is a CodefixProposalPayload
      // — render it as a CodefixProposalCard. The card hydrates from
      // /api/codefix-proposals/<id> on mount and exposes a Discard button.
      // Per-repo aliases mean the tool name varies, so we use the predicate.
      if (isDraftPrToolName(item.name) && item.result) {
        try {
          const payload = JSON.parse(item.result) as CodefixProposalPayload;
          // A non-empty proposal_id means the draft was accepted and
          // persisted. A validation-failure result carries proposal_id:""
          // (plus validation_errors); it must NOT render as a proposal card —
          // fall through to the raw tool card so the failed attempt is visible
          // as activity, not an empty approve/decline card.
          if (payload && typeof payload.proposal_id === "string" && payload.proposal_id.length > 0) {
            return (
              <div data-testid="triagent-proposal-card" data-proposal-kind="codefix">
                <CodefixProposalCard payload={payload} />
              </div>
            );
          }
        } catch {
          /* fall through to the raw tool card if the JSON is unparseable */
        }
      }
      // The create_github_issue tool result is an issue-only proposal —
      // no dedicated card; it renders as the standard ToolCard. Tag it so
      // the e2e transcript can locate it as the fourth proposal alongside
      // the three dedicated cards.
      if (isCreateGithubIssueToolName(item.name) && item.result) {
        return (
          <div data-testid="triagent-proposal-card" data-proposal-kind="github_issue">
            <ToolCard
              toolId={item.toolId}
              name={item.name}
              input={item.input}
              result={item.result}
              pending={item.result === undefined}
              nested={item.children}
            />
          </div>
        );
      }
      // The strategies summarize tool's result IS the formal
      // investigation summary. Render the verdict as an amber card and
      // (when present) the evidence as a sibling dark-blue card —
      // operator-facing TL;DR up top, reviewer-facing proof beneath.
      // Multiple calls produce multiple block pairs, traceable in
      // order. The wire result is the SDK's structured-output JSON
      // (`{markdown, evidence_markdown?}`); we unwrap it here. While
      // the call is still in flight (no result yet), fall back to the
      // regular tool card so the operator sees the spinner.
      if (item.name === SUMMARIZE_TOOL_NAME && item.result) {
        const parsed = extractSummarizeResult(item.result);
        if (parsed) {
          return (
            <div className="space-y-2">
              <div
                id={toolAnchorId(item.toolId)}
                className="relative rounded border border-amber-900/60 bg-amber-950/30 px-3 py-2"
              >
                <CopyButton
                  text={parsed.markdown}
                  className="absolute right-2 top-2"
                  tone="amber"
                />
                <div className="mb-1 text-xs uppercase tracking-wide text-amber-400/80">
                  summary
                </div>
                <Markdown text={parsed.markdown} />
              </div>
              {parsed.evidence && (
                <div className="relative rounded border border-blue-900/60 bg-blue-950/30 px-3 py-2">
                  <CopyButton
                    text={parsed.evidence}
                    className="absolute right-2 top-2"
                    tone="blue"
                  />
                  <div className="mb-1 text-xs uppercase tracking-wide text-blue-300/80">
                    evidence
                  </div>
                  <Markdown text={parsed.evidence} />
                </div>
              )}
            </div>
          );
        }
        // Fall through to the regular tool card if the result didn't
        // unwrap — better to surface raw JSON than to silently drop a
        // summary the operator was expecting.
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
