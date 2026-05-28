// EventEnvelope mirrors the JSON shape the Go server emits over SSE.
// Optional fields are present per kind; the reducer below folds a stream
// of envelopes into a structured transcript the UI can render directly.

import type { RehydrateStatePayload } from "@/lib/api";

// AutoModePhase enumerates the auto-operator lifecycle states the Go
// server emits on auto_mode_state envelopes. Kept narrow on purpose —
// matches the AutoModePayload.Phase comment in
// investigate/internal/server/events.go.
export type AutoModePhase =
  | "started"
  | "paused"
  | "resumed"
  | "finished"
  | "aborted";

// AutoModePayload mirrors the Go AutoModePayload struct
// (investigate/internal/server/events.go). Rides on auto_mode_state
// envelopes. The useAutoMode hook folds these into the UI's auto-mode
// state; the transcript reducer ignores them.
export type AutoModePayload = {
  phase: AutoModePhase;
  reason?: string;
  takenOverBy?: "human" | "operator";
  error?: string;
  warning?: boolean;
};

export type EventEnvelope = {
  seq: number;
  kind:
    | "system"
    | "assistant"
    | "tool_use"
    | "tool_result"
    | "tool_status"
    | "result"
    | "error"
    | "user"
    | "end"
    | "push_state"
    | "label"
    | "rehydrate_state"
    | "codefix_pr_state"
    | "auto_mode_state"
    | "usage";
  timestamp: string;
  sessionId?: string;
  subtype?: string;
  text?: string;
  toolId?: string;
  toolName?: string;
  toolInput?: Record<string, unknown>;
  parentToolId?: string;
  // origin labels who authored a user envelope. "human" → the chat
  // composer; "operator" → the auto-operator's send_message tool. The
  // server stamps this on the EventEnvelope so the transcript can style
  // operator-authored user turns distinctly (pink envelope). Absent on
  // legacy user envelopes; treat undefined as "human".
  origin?: "human" | "operator";
  // Set on push_state envelopes only. The server emits these as the
  // async session-push goroutine transitions phase.
  pushState?: {
    phase: "pushing" | "success" | "error";
    startedAt?: string;
    url?: string;
    branch?: string;
    base?: string;
    error?: string;
  };
  // Set on rehydrate_state envelopes only. The server emits these as
  // the rehydrate orchestration transitions phase (started →
  // succeeded | failed).
  rehydrateState?: RehydrateStatePayload;
  // codefixPRState is set on codefix_pr_state envelopes. The launcher's
  // PR-state poller emits one whenever a tracked codefix PR transitions
  // state (open → merged/closed). The streaming reducer routes it into
  // a per-URL state map; the CodefixProposalCard reads from there.
  codefixPRState?: {
    url: string;
    state: "open" | "merged" | "closed" | "draft" | "";
    mergedAt?: string;
    closedAt?: string;
  };
  // autoMode is set on auto_mode_state envelopes only. The server emits
  // these as the AutoOperator transitions phase (started → paused |
  // resumed | finished | aborted). The useAutoMode hook folds these
  // into UI state; the transcript reducer ignores them.
  autoMode?: AutoModePayload;
  // usage rides on `usage` envelopes (per-API-call token tally, summed
  // across the invocation), on assistant envelopes for the message-level
  // tally on text replies (informational, not summed), and on result
  // envelopes legacy-only (older sessions). Optional because older /
  // unusual claude builds may omit it. The sidebar + session footer sum
  // `usage`-envelope usage to render running totals.
  usage?: Usage;
  // costUsd rides on result envelopes only. Summed across result
  // envelopes for the per-investigation total.
  costUsd?: number;
};

// Usage mirrors the Go claude.Usage struct (investigate/internal/claude/events.go).
// Every component is in tokens; cache_creation_input_tokens and
// cache_read_input_tokens are distinct from input/output so frontends
// that want a cost-aware "in / out / cache" breakdown can have it.
export type Usage = {
  inputTokens?: number;
  outputTokens?: number;
  cacheCreationInputTokens?: number;
  cacheReadInputTokens?: number;
};

// NestedEvent is a child of a tool_call card — surfaces as collapsed rows
// inside the parent. Used to render sub-agent activity (e.g. claude reading
// files inside a git MCP's analyze_change tool) without polluting the main
// transcript.
export type NestedEvent =
  | { kind: "text"; id: string; text: string; ts: string }
  | { kind: "status"; id: string; text: string; ts: string }
  | {
      kind: "tool";
      id: string;
      toolId: string;
      name: string;
      input?: Record<string, unknown>;
      result?: string;
      errorText?: string;
      ts: string;
      tsEnd?: string;
    };

export type TranscriptItem =
  // origin labels who authored a user envelope. Carried through from
  // the wire EventEnvelope so the transcript renderer can style
  // operator-authored user turns distinctly (pink envelope chrome).
  | { kind: "user"; id: string; text: string; origin?: "human" | "operator" }
  | { kind: "assistant"; id: string; text: string }
  | {
      kind: "tool_call";
      id: string;
      toolId: string;
      name: string;
      input?: Record<string, unknown>;
      result?: string;
      startedAt: string; // from tool_use envelope timestamp
      endedAt?: string; // from tool_result envelope timestamp; absent while running
      children?: NestedEvent[];
    }
  | { kind: "error"; id: string; text: string }
  // auto_mode_divider is a synthetic transcript item folded out of
  // auto_mode_state envelopes by applyEvent. Rendered as an inline
  // "— auto mode <phase> —" chip in the transcript so the phase
  // transitions interleave with chat turns in the order they happened.
  // The useAutoMode hook still reads auto_mode_state envelopes for the
  // composer-gating UI state; the divider is purely visual.
  | {
      kind: "auto_mode_divider";
      id: string;
      payload: AutoModePayload;
      timestamp: string;
    };

// Wire-format name of the strategies summarize tool. SessionView
// renders this tool's result inline as the amber conclusion block —
// the markdown audit trail is the conclusion content, no separate
// marker tool needed. Multiple calls (e.g. one per material follow-up)
// each get their own block, traceable in order.
export const SUMMARIZE_TOOL_NAME = "mcp__triagent-strategies__summarize";

// Wire-format name of the playbook-proposal draft tool. SessionView
// renders this tool's result inline as a diff card with Approve /
// Decline controls; the structured-output JSON carries
// {proposal_id, base_yaml, new_yaml, …}. The agent
// is instructed (via the playbook_proposal meta-playbook + system
// prompt) to call this preemptively, no chat-side ask required.
export const PROPOSE_PLAYBOOK_DRAFT_TOOL_NAME =
  "mcp__triagent-strategies__playbook_proposal_draft";

// Wire-format name of the wiki-proposal draft tool. SessionView
// renders this tool's result inline as a WikiProposalCard with
// markdown preview + Approve / Decline controls; the
// structured-output JSON carries
// {kind: "wiki_proposal_draft", proposal_id, slug, base_md,
//  new_md, is_new, ...}.
export const PROPOSE_WIKI_DRAFT_TOOL_NAME =
  "mcp__triagent-wiki__propose_wiki_draft";

// Wire-format suffix of the per-repo draft_pr tool. Since each linked
// repo gets its own triagent-git-<alias> MCP server (one process per repo),
// the full tool name varies (e.g. `mcp__triagent-git-zeebe__draft_pr`,
// `mcp__triagent-git-operate__draft_pr`). isDraftPrToolName matches the
// prefix + suffix shape without enumerating aliases.
export const DRAFT_PR_TOOL_NAME_SUFFIX = "__draft_pr";

export function isDraftPrToolName(name: string): boolean {
  return name.startsWith("mcp__triagent-git-") && name.endsWith(DRAFT_PR_TOOL_NAME_SUFFIX);
}

// Wire-format suffix of the per-repo create_github_issue tool. Like
// draft_pr, the full name varies per linked repo
// (mcp__triagent-git-<alias>__create_github_issue). Mirrors the backend
// isCreateGithubIssueToolName helper in
// internal/server/handlers_codefix.go.
export const CREATE_GITHUB_ISSUE_TOOL_NAME_SUFFIX = "__create_github_issue";

export function isCreateGithubIssueToolName(name: string): boolean {
  return (
    name.startsWith("mcp__triagent-git-") &&
    name.endsWith(CREATE_GITHUB_ISSUE_TOOL_NAME_SUFFIX)
  );
}

// CodefixProposalPayload is the JSON shape the launcher emits on
// GET /api/codefix-proposals/{id} and the result body of a draft_pr
// tool call. The CodefixProposalCard renders this directly.
export type CodefixProposalPayload = {
  proposal_id: string;
  repo: string;            // owner/name
  issue_url: string;
  issue_number: number;
  pr_url: string;
  pr_number: number;
  branch_name: string;
  summary: string;
  // investigation_id and created_at land on entries persisted by
  // newer launchers — the get-by-id handler doesn't depend on them
  // but the list handler relies on both for traceability + sort.
  investigation_id?: string;
  created_at?: string;
};

// CodefixProposalListing is the GET /api/codefix-proposals list-item
// shape: the payload joined with current PR state from the
// launcher's poller cache. The repos sidenav consumes this.
export type CodefixProposalListing = CodefixProposalPayload & {
  pr_state?: string;
};

// applyEvent folds a single envelope into the running transcript. tool_use
// becomes a tool_call card; the matching tool_result fills in `.result`
// in-place. Events with parentToolId attach as nested children of that
// parent tool_call rather than entering the top-level list.
// system + end are intentionally not rendered as transcript items — they
// drive the status pill instead.
export function applyEvent(
  items: TranscriptItem[],
  ev: EventEnvelope,
): TranscriptItem[] {
  // Nested events route to a parent tool_call's `children` array. The
  // parent must already exist (parent tool_use precedes its children in
  // the wire ordering); if it doesn't, we drop the event silently rather
  // than synthesising a top-level item that would confuse the UI.
  if (ev.parentToolId && (ev.kind === "tool_use" || ev.kind === "tool_result" || ev.kind === "assistant" || ev.kind === "tool_status")) {
    return items.map((it) =>
      it.kind === "tool_call" && it.toolId === ev.parentToolId
        ? { ...it, children: applyNested(it.children ?? [], ev) }
        : it,
    );
  }

  switch (ev.kind) {
    case "user":
      return [
        ...items,
        { kind: "user", id: idFor(ev), text: ev.text ?? "", origin: ev.origin },
      ];
    case "assistant":
      return [
        ...items,
        { kind: "assistant", id: idFor(ev), text: ev.text ?? "" },
      ];
    case "tool_use":
      return [
        ...items,
        {
          kind: "tool_call",
          id: idFor(ev),
          toolId: ev.toolId ?? "",
          name: ev.toolName ?? "",
          input: ev.toolInput,
          startedAt: ev.timestamp,
        },
      ];
    case "tool_result":
      return items.map((it) =>
        it.kind === "tool_call" && it.toolId === ev.toolId
          ? { ...it, result: ev.text ?? "", endedAt: ev.timestamp }
          : it,
      );
    case "result":
      // No transcript item: the result envelope used to render an
      // amber summary block, but it fired per-turn — every follow-up
      // got framed as a summary. The formal conclusion is now opt-in
      // via the conclude_investigation marker tool.
      return items;
    case "error":
      return [...items, { kind: "error", id: idFor(ev), text: ev.text ?? "" }];
    case "auto_mode_state":
      // auto_mode_state drives two UI surfaces. The useAutoMode hook
      // folds it into the composer-gating state (Take over / Resume /
      // Restart). It ALSO produces a synthetic transcript item — a
      // small pink-bordered divider chip — so the operator can see in
      // the chat scroll where the auto-operator paused, resumed,
      // finished, or aborted. We render the divider via the existing
      // TranscriptItem pipeline so it interleaves with user/assistant
      // turns by seq order without a parallel render pass.
      if (!ev.autoMode) return items;
      return [
        ...items,
        {
          kind: "auto_mode_divider",
          id: idFor(ev),
          payload: ev.autoMode,
          timestamp: ev.timestamp,
        },
      ];
    case "system":
    case "end":
    case "push_state":
    case "label":
    case "rehydrate_state":
    case "tool_status":
    case "codefix_pr_state":
    case "usage":
      // usage envelopes carry per-API-call token tallies for the
      // running totals — SessionWorkspace folds them into investigation
      // state directly. They are not transcript items.
      return items;
  }
}

function applyNested(children: NestedEvent[], ev: EventEnvelope): NestedEvent[] {
  switch (ev.kind) {
    case "assistant":
      return [
        ...children,
        { kind: "text", id: idFor(ev), text: ev.text ?? "", ts: ev.timestamp },
      ];
    case "tool_status":
      return [
        ...children,
        { kind: "status", id: idFor(ev), text: ev.text ?? "", ts: ev.timestamp },
      ];
    case "tool_use":
      return [
        ...children,
        {
          kind: "tool",
          id: idFor(ev),
          toolId: ev.toolId ?? "",
          name: ev.toolName ?? "",
          input: ev.toolInput,
          ts: ev.timestamp,
        },
      ];
    case "tool_result":
      return children.map((c) =>
        c.kind === "tool" && c.toolId === ev.toolId
          ? { ...c, result: ev.text ?? "", tsEnd: ev.timestamp }
          : c,
      );
    default:
      return children;
  }
}

function idFor(ev: EventEnvelope): string {
  return `${ev.kind}-${ev.seq}`;
}

// reduce folds an entire batch of envelopes into a transcript. Used for
// the SSE backlog replay on connect — cheaper than dispatching N times
// through React's state setter.
export function reduce(events: EventEnvelope[]): TranscriptItem[] {
  return events.reduce<TranscriptItem[]>(applyEvent, []);
}

// SessionStatus is the high-level state shown by the status pill. Driven
// by `streaming` from /api/investigations and the `end` event over SSE.
export type SessionStatus = "starting" | "streaming" | "idle" | "error";

// parseToolName splits the wire-format tool name `mcp__<server>__<tool>`
// into its server alias (triagent-k8s, prom, triagent-strategies, example-docs,
// triagent-git-<alias>) and the short tool name. Tools without the mcp__ prefix
// come back with server=null and the original name as `short`.
export function parseToolName(name: string): { server: string | null; short: string } {
  if (!name.startsWith("mcp__")) return { server: null, short: name };
  const rest = name.slice(5);
  const idx = rest.indexOf("__");
  if (idx < 0) return { server: null, short: rest };
  return { server: rest.slice(0, idx), short: rest.slice(idx + 2) };
}
