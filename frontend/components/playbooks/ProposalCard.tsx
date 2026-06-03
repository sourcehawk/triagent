"use client";

import { useEffect, useState } from "react";
import { api, ApiError } from "@/lib/api";
import { Spinner } from "@/components/shared/Spinner";
import { ProposalBodyTabs } from "@/components/playbooks/ProposalBodyTabs";

// Payload returned by the triagent-strategies/playbook_proposal_draft tool.
// Keep this in lockstep with proposePlaybookDraftOut in
// mcp/internal/strategies/tools_proposal.go.
export type ProposalDraftPayload = {
  proposal_id: string;
  playbook_id: string;
  base_yaml?: string;
  new_yaml: string;
  why?: string;
  message?: string;
};

type Status =
  // Initial state on mount: we ask the server whether the proposal is
  // still pending before showing any actions, so an old chat reloaded
  // after the operator approved/declined elsewhere doesn't offer
  // stale buttons that would 404.
  | { kind: "checking" }
  | { kind: "pending" }
  | { kind: "approving" }
  | { kind: "approved"; id: string; version: string; activated: boolean }
  | { kind: "declining" }
  | { kind: "declined" }
  // Server has no draft and no resolution marker — we can't tell if
  // it was approved or declined, but actions are no longer applicable.
  | { kind: "resolved-unknown" }
  | { kind: "error"; message: string; errors?: string[] };

type Props = {
  payload: ProposalDraftPayload;
  // Sends a follow-up message back to the agent. Reused for the
  // "decline + ask the agent to refine" flow — the operator types
  // their refinement notes, we POST to /api/investigations/{id}/messages
  // via this callback. SessionView already has a sendMessage handler
  // wired up, we just borrow it.
  onSendRefinement: (text: string) => Promise<void> | void;
  // dismissed: the proposal was approved or declined elsewhere
  // (e.g. via the editor's AI proposal tab). When true, the card hides
  // its action UI and shows a passive "handled outside this card"
  // notice so the operator can't double-click and hit a stale 404.
  dismissed?: boolean;
  // onResolved fires after a successful approve or decline so a
  // parent that's tracking the proposal lifetime (the editor's AI
  // proposal tab, lifted-state surfaces) can clean up. Optional —
  // the chat-only usage doesn't need it.
  onResolved?: (kind: "approved" | "declined") => void;
};

export function ProposalCard({
  payload,
  onSendRefinement,
  dismissed = false,
  onResolved,
}: Props) {
  const [status, setStatus] = useState<Status>({ kind: "checking" });
  const [refinement, setRefinement] = useState("");
  const isNew = !payload.base_yaml || payload.base_yaml.trim() === "";

  // On mount, ask the server whether this proposal is still pending.
  // Reloading a chat after the operator approved/declined elsewhere
  // would otherwise show stale Approve/Decline buttons; the server
  // tracks the outcome via a tiny resolution ledger.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await api.getPlaybookProposal(payload.proposal_id);
        if (cancelled) return;
        if (res.status === "approved") {
          setStatus({
            kind: "approved",
            id: res.id ?? payload.playbook_id,
            version: res.version ?? "",
            activated: res.activated ?? false,
          });
        } else if (res.status === "declined") {
          setStatus({ kind: "declined" });
        } else {
          setStatus({ kind: "pending" });
        }
      } catch (e) {
        if (cancelled) return;
        // 404: no draft and no resolution marker (e.g. older proposals
        // resolved before the ledger existed). Hide actions but admit
        // we don't know which way it went.
        if (e instanceof ApiError && e.status === 404) {
          setStatus({ kind: "resolved-unknown" });
          return;
        }
        // Network or other transient: fall back to pending so the
        // operator can still act. The approve/decline endpoints
        // themselves are idempotent against the ledger.
        setStatus({ kind: "pending" });
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [payload.proposal_id, payload.playbook_id]);

  async function approve() {
    setStatus({ kind: "approving" });
    try {
      const res = await api.approvePlaybookProposal(payload.proposal_id);
      if (res.ok) {
        setStatus({
          kind: "approved",
          id: res.id,
          version: res.version,
          activated: res.activated,
        });
        window.dispatchEvent(new CustomEvent("c1:playbook-proposals-changed"));
        onResolved?.("approved");
      } else {
        setStatus({
          kind: "error",
          message: "validation failed",
          errors: res.errors,
        });
      }
    } catch (e) {
      setStatus({
        kind: "error",
        message: e instanceof ApiError ? e.message : String(e),
      });
    }
  }

  async function decline() {
    setStatus({ kind: "declining" });
    try {
      const note = refinement.trim();
      // Dual-write the note: the decline endpoint persists it into the
      // .resolved marker (read by the strategies MCP's list_proposals
      // tool and the dispatch prompt's auto-injected proposal state),
      // and the existing chat follow-up still goes so the master agent
      // sees it in conversation context. The two channels serve
      // different consumers — sub-agent reads marker, master reads chat.
      await api.declinePlaybookProposal(payload.proposal_id, note || undefined);
      if (note) {
        await onSendRefinement(note);
      }
      setStatus({ kind: "declined" });
      window.dispatchEvent(new CustomEvent("c1:playbook-proposals-changed"));
      onResolved?.("declined");
    } catch (e) {
      setStatus({
        kind: "error",
        message: e instanceof ApiError ? e.message : String(e),
      });
    }
  }

  return (
    <div className="rounded border border-sky-900/60 bg-sky-950/20 px-3 py-2">
      {/* Header: id + version transition */}
      <div className="mb-2 flex items-baseline justify-between gap-2">
        <div className="flex items-baseline gap-2">
          <span className="text-xs uppercase tracking-wide text-sky-400/80">
            playbook proposal
          </span>
          <span className="font-mono text-xs text-zinc-100">
            {payload.playbook_id}
          </span>
          <span className="font-mono text-xs text-zinc-500">
            {isNew ? "new playbook" : "current → proposed"}
          </span>
        </div>
      </div>

      {payload.why && (
        <p className="mb-2 text-xs italic text-zinc-400">{payload.why}</p>
      )}

      <div className="mb-2">
        <ProposalBodyTabs payload={payload} />
      </div>

      {/* Footer: status / actions. Dismissed proposals (handled
          outside this card — typically via the editor's AI proposal
          tab) hide the action row entirely; we still want the diff
          visible above for context, just not a stale Approve/Decline
          pair the operator could click and hit a 404 with. */}
      {status.kind === "checking" && (
        <div className="flex items-center gap-2 text-xs text-zinc-500">
          <Spinner className="h-3 w-3" /> checking proposal status…
        </div>
      )}
      {dismissed && status.kind === "pending" && (
        <div className="rounded border border-zinc-800 bg-zinc-900/40 px-2 py-1 text-xs text-zinc-400">
          Handled outside this card — see the editor's AI proposal tab.
        </div>
      )}
      {status.kind === "pending" && !dismissed && (
        <div className="space-y-2">
          <textarea
            value={refinement}
            onChange={(e) => setRefinement(e.target.value)}
            placeholder="optional: ask the agent to refine before declining (e.g. 'add a branch for X', 'tighten symptom wording')"
            rows={2}
            className="w-full resize-y rounded border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-100 placeholder-zinc-600 focus:border-zinc-600 focus:outline-none"
          />
          {/* Local-scope tagline. Approval lands in the operator's
              user playbooks dir; sharing happens via push-as-PR. */}
          <p className="text-xs leading-relaxed text-amber-400/80">
            Approval is <strong>local-only</strong> — the new version
            lands in your user playbooks dir{" "}
            (<span className="font-mono text-zinc-400">
              ~/.config/c1/plugins/investigate/playbooks/
            </span>
            ) and won't reach other operators until you push it as a
            PR via the playbook editor.
          </p>
          <div className="flex items-end justify-between gap-2">
            <p className="text-xs text-zinc-500">
              File:{" "}
              <span className="font-mono text-zinc-300">
                {payload.playbook_id}.yaml
              </span>
            </p>
            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={decline}
                className="rounded border border-zinc-700 px-2.5 py-1 text-xs text-zinc-300 transition hover:border-zinc-500 hover:text-zinc-100"
              >
                {refinement.trim() ? "decline + send notes" : "decline"}
              </button>
              <button
                type="button"
                data-testid="triagent-proposal-approve"
                onClick={() => void approve()}
                className="rounded bg-emerald-600 px-2.5 py-1 text-xs font-medium text-white transition hover:bg-emerald-500"
              >
                approve & activate locally
              </button>
            </div>
          </div>
        </div>
      )}

      {status.kind === "approving" && (
        <div className="flex items-center gap-2 text-xs text-zinc-400">
          <Spinner className="h-3 w-3" /> approving…
        </div>
      )}
      {status.kind === "declining" && (
        <div className="flex items-center gap-2 text-xs text-zinc-400">
          <Spinner className="h-3 w-3" /> declining…
        </div>
      )}
      {status.kind === "approved" && (
        <div className="rounded border border-emerald-900/60 bg-emerald-950/40 px-2 py-1 text-xs text-emerald-300">
          ✓ approved as{" "}
          <span className="font-mono">
            {status.id}.yaml
          </span>
          {" "}— local active pointer updated. Toggle active anytime from the editor.
        </div>
      )}
      {status.kind === "declined" && (
        <div className="rounded border border-zinc-800 bg-zinc-900/40 px-2 py-1 text-xs text-zinc-400">
          declined.
          {refinement.trim() && " refinement notes sent to the agent."}
        </div>
      )}
      {status.kind === "resolved-unknown" && (
        <div className="rounded border border-zinc-800 bg-zinc-900/40 px-2 py-1 text-xs text-zinc-400">
          This proposal is no longer pending — it was approved or declined
          previously. Ask the agent for a fresh draft if you want to revisit.
        </div>
      )}
      {status.kind === "error" && (
        <div className="rounded border border-red-900/60 bg-red-950/40 px-2 py-1 text-xs text-red-200/90">
          {status.message}
          {status.errors && status.errors.length > 0 && (
            <ul className="ml-3 list-disc">
              {status.errors.map((e, i) => (
                <li key={i}>{e}</li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}

