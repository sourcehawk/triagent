"use client";

import { Spinner } from "@/components/shared/Spinner";
import { CheckIcon, ExternalLinkIcon } from "@/components/shared/Icons";
import { relativeTime } from "@/lib/relative-time";
import { formatCostUSD, formatTokens, totalTokens } from "@/lib/usage";
import { type SessionStatus } from "@/lib/events";

export function StatusPill({
  status,
  archived,
}: {
  status: SessionStatus;
  archived: boolean;
}) {
  // Archived overrides the SSE-stream status — once a session is
  // wound down the stream sits idle but "idle" reads as "ready for the
  // next prompt", which is misleading. Show "archived" instead.
  if (archived) {
    return (
      <span className="inline-flex items-center gap-1.5 rounded-full bg-zinc-800 px-2 py-0.5 text-xs text-zinc-400">
        archived
      </span>
    );
  }
  switch (status) {
    case "starting":
      return (
        <span className="inline-flex items-center gap-1.5 rounded-full bg-zinc-800 px-2 py-0.5 text-xs text-zinc-300">
          <Spinner className="h-3 w-3" /> starting
        </span>
      );
    case "streaming":
      return (
        <span className="inline-flex items-center gap-1.5 rounded-full bg-sky-900/50 px-2 py-0.5 text-xs text-sky-300">
          <Spinner className="h-3 w-3" /> streaming
        </span>
      );
    case "idle":
      return (
        <span className="inline-flex items-center gap-1.5 rounded-full bg-emerald-900/50 px-2 py-0.5 text-xs text-emerald-300">
          idle
        </span>
      );
    case "error":
      return (
        <span className="inline-flex items-center gap-1.5 rounded-full bg-red-900/50 px-2 py-0.5 text-xs text-red-300">
          error
        </span>
      );
  }
}

// PushedBadge renders the "this session is on the upstream sessions
// repo" indicator. The PR's lifecycle (open / merged / closed) is
// pulled from `gh pr list` by the backend's RefreshPRStates and
// drives the colour + label here.
export function PushedBadge({
  pushUrl,
  prState,
  prMergedAt,
  prClosedAt,
}: {
  pushUrl?: string;
  prState?: "open" | "merged" | "closed";
  prMergedAt?: string;
  prClosedAt?: string;
}) {
  if (!pushUrl) {
    // Reconciled session: we know it's upstream (the slug exists in
    // the local sessions clone) but we never captured the PR URL.
    return (
      <span
        className="inline-flex items-center gap-1 text-xs text-emerald-400"
        title="this session is on the upstream sessions repo (PR URL unknown — pushed before the launcher captured it)"
      >
        <CheckIcon className="h-3 w-3" />
        synced
      </span>
    );
  }
  const visual = pushedBadgeTone(prState);
  const when =
    prState === "merged" && prMergedAt
      ? `merged ${relativeTime(prMergedAt)}`
      : prState === "closed" && prClosedAt
        ? `closed ${relativeTime(prClosedAt)}`
        : "";
  const title = (when ? `${visual.title} (${when})` : visual.title) + ` — ${pushUrl}`;
  return (
    <a
      href={pushUrl}
      target="_blank"
      rel="noreferrer"
      className={
        "inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-xs transition " +
        visual.classes
      }
      title={title}
    >
      <CheckIcon className="h-3 w-3" />
      {visual.label}
      <ExternalLinkIcon className="h-3 w-3 opacity-70" />
    </a>
  );
}

function pushedBadgeTone(state?: "open" | "merged" | "closed") {
  switch (state) {
    case "merged":
      return {
        label: "merged",
        title: "PR merged",
        classes:
          "border-violet-900/60 bg-violet-950/30 text-violet-300 hover:border-violet-700 hover:text-violet-200",
      };
    case "closed":
      return {
        label: "closed",
        title: "PR closed without merging — push again to retry",
        classes:
          "border-zinc-700 bg-zinc-900/40 text-zinc-400 hover:border-zinc-500 hover:text-zinc-200",
      };
    case "open":
    default:
      return {
        label: "PR open",
        title: "PR open — review pending",
        classes:
          "border-emerald-900/60 bg-emerald-950/40 text-emerald-300 hover:border-emerald-700 hover:text-emerald-200",
      };
  }
}

// SessionActionButton renders one of the session-level trigger
// buttons in the action row above the chat textarea. Tone selects
// the colour scheme (emerald = wiki, sky = playbook, amber = codefix,
// rose = bug-report). Disabled state uses zinc with a not-allowed
// cursor; the hover tooltip carries either the explanatory disabled
// reason or the enabled-state hint.
export function SessionActionButton({
  tone,
  enabled,
  disabledReason,
  enabledTitle,
  onClick,
  label,
}: {
  tone: "emerald" | "sky" | "amber" | "rose";
  enabled: boolean;
  disabledReason: string;
  enabledTitle: string;
  onClick: () => void;
  label: string;
}) {
  const enabledClasses =
    tone === "emerald"
      ? "rounded border border-emerald-700 bg-emerald-900/30 px-2.5 py-1 text-xs text-emerald-200 transition hover:border-emerald-500 hover:bg-emerald-900/50"
      : tone === "sky"
        ? "rounded border border-sky-700 bg-sky-900/30 px-2.5 py-1 text-xs text-sky-200 transition hover:border-sky-500 hover:bg-sky-900/50"
        : tone === "amber"
          ? "rounded border border-amber-700 bg-amber-900/30 px-2.5 py-1 text-xs text-amber-200 transition hover:border-amber-500 hover:bg-amber-900/50"
          : "rounded border border-rose-700 bg-rose-900/30 px-2.5 py-1 text-xs text-rose-200 transition hover:border-rose-500 hover:bg-rose-900/50";
  return (
    <button
      type="button"
      onClick={enabled ? onClick : undefined}
      disabled={!enabled}
      title={enabled ? enabledTitle : disabledReason}
      className={
        enabled
          ? enabledClasses
          : "rounded border border-zinc-800 px-2.5 py-1 text-xs text-zinc-600 cursor-not-allowed"
      }
    >
      {label}
    </button>
  );
}

// UsageReadout renders the per-session token + cost total at the right
// end of the action row, above the chat textarea. Reads the running
// aggregate the launcher stamps onto Investigation snapshots (summed
// from every result envelope claude emitted). Hidden when nothing has
// been spent yet so a fresh session doesn't show a stray "0 tok · $0.00".
export function UsageReadout({
  usage,
  costUsd,
}: {
  usage?: { inputTokens?: number; outputTokens?: number; cacheCreationInputTokens?: number; cacheReadInputTokens?: number };
  costUsd?: number;
}) {
  const tokens = totalTokens(usage);
  const cost = costUsd ?? 0;
  if (tokens === 0 && cost === 0) return null;
  return (
    <span
      data-testid="triagent-usage-readout"
      className="ml-auto font-mono text-xs text-zinc-500"
      title={`Total token usage across this session, summed from every claude turn. Cache reads price at ~10% of fresh input tokens, so cost can be much lower than the raw token count suggests.`}
    >
      {formatTokens(tokens)} tok · {formatCostUSD(cost)}
    </span>
  );
}
