"use client";

import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import type { CodefixProposalPayload } from "@/lib/events";
import { useCodefixPRState } from "@/components/codefix/CodefixPRStateProvider";

type Props = {
  payload: CodefixProposalPayload;
};

type PRState = "draft" | "open" | "merged" | "closed" | "" | "error";

// CodefixProposalCard renders the lifecycle of one in-flight or
// landed codefix proposal. Minimal frame: no diff (review happens on
// GitHub), no Approve button. The card hydrates from the launcher on
// mount; live state transitions arrive via SSE in a later task and
// reset our local `state` via prop / context wiring.
export function CodefixProposalCard({ payload }: Props) {
  const [state, setState] = useState<PRState>("draft");
  const [discarding, setDiscarding] = useState(false);
  const liveState = useCodefixPRState(payload.pr_url);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const fresh = await api.getCodefixProposal(payload.proposal_id);
        if (!cancelled) setState((fresh.pr_state as PRState) || "draft");
      } catch {
        if (!cancelled) setState("error");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [payload.proposal_id]);

  async function onDiscard() {
    setDiscarding(true);
    try {
      await api.discardCodefixProposal(payload.proposal_id);
      setState("closed");
    } finally {
      setDiscarding(false);
    }
  }

  // SSE-driven state (from CodefixPRStateProvider) overlays the
  // locally-fetched state once available. The local state remains the
  // source of truth before the first SSE tick and after a Discard click.
  const effectiveState = (liveState?.state as PRState) || state;
  const lifecycle = renderLifecycle(effectiveState);

  return (
    <div className="rounded border border-amber-900/60 bg-amber-950/30 p-3 text-sm">
      <div className="mb-1 text-xs uppercase tracking-wide text-amber-300">
        codefix
      </div>
      <div className="space-y-1.5">
        <div>
          {lifecycle.badge}{" "}
          <a
            href={payload.pr_url}
            target="_blank"
            rel="noreferrer"
            className="text-amber-200 underline-offset-2 hover:underline"
          >
            review on GitHub →
          </a>
        </div>
        <div className="text-zinc-300">{payload.summary}</div>
        <div className="text-xs text-zinc-500">
          Filed:{" "}
          <a
            href={payload.issue_url}
            target="_blank"
            rel="noreferrer"
            className="underline-offset-2 hover:underline"
          >
            {payload.repo}#{payload.issue_number}
          </a>
        </div>
      </div>
      {lifecycle.discardable && (
        <div className="mt-2">
          <button
            type="button"
            disabled={discarding}
            onClick={onDiscard}
            className="rounded border border-zinc-700 px-2 py-1 text-xs text-zinc-300 transition hover:border-zinc-500 disabled:opacity-50"
          >
            Discard
          </button>
        </div>
      )}
    </div>
  );
}

function renderLifecycle(state: PRState): {
  badge: React.ReactNode;
  discardable: boolean;
} {
  switch (state) {
    case "draft":
      return {
        badge: <span className="font-medium text-amber-200">Draft PR opened —</span>,
        discardable: true,
      };
    case "open":
      return {
        badge: <span className="font-medium text-sky-300">PR open for review —</span>,
        discardable: true,
      };
    case "merged":
      return {
        badge: <span className="font-medium text-violet-300">Merged —</span>,
        discardable: false,
      };
    case "closed":
      return {
        badge: <span className="font-medium text-zinc-400">Closed without merging —</span>,
        discardable: false,
      };
    case "error":
      return {
        badge: <span className="font-medium text-zinc-400">PR state unknown —</span>,
        discardable: false,
      };
    default:
      return {
        badge: <span>—</span>,
        discardable: true,
      };
  }
}
