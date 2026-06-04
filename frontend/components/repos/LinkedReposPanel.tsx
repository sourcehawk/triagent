"use client";

import { useEffect, useState } from "react";
import { type Investigation } from "@/lib/api";
import { ManageReposModal } from "./LinkedReposPanel.modal";
import { PendingReposList, ActiveReposList } from "./LinkedReposPanel.list";

type Props = {
  // Active investigation, when one is open. Drives the read-only "linked
  // for this session" list. Null when no investigation is active — the
  // panel still renders with the manage button so users can curate the
  // list before starting a new investigation.
  investigation: Investigation | null;
  // Bumped by the parent on session changes so we re-fetch the global
  // repo lists if the user navigates away and back.
  refreshNonce?: number;
};

const EXPANDED_KEY = "triagent.linkedrepos.expanded";

// MIN_DESCRIPTION_LENGTH mirrors repos.MinDescriptionLength on the Go side.
// Hard-coded constant rather than fetched from the server to avoid a
// network round-trip on form mount; if the backend ever raises the
// minimum, update here too. The backend rejection is the source of truth.
export const MIN_DESCRIPTION_LENGTH = 30;

function readExpanded(): boolean {
  if (typeof window === "undefined") return false;
  return window.localStorage.getItem(EXPANDED_KEY) === "1";
}

export function LinkedReposPanel({ investigation, refreshNonce }: Props) {
  const [manageOpen, setManageOpen] = useState(false);
  // Collapsed by default so the panel only takes the header strip
  // (~40px) at the bottom of the sidebar; expanding it overlays
  // upward into the history list. Persisted so an operator who keeps
  // it pinned open doesn't have to re-toggle on every reload.
  const [expanded, setExpanded] = useState<boolean>(readExpanded);

  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem(EXPANDED_KEY, expanded ? "1" : "0");
  }, [expanded]);

  // Listen for the launcher-wide "open the manage-repos modal" event.
  // The /repos page's primary "+ add repository" sidebar action
  // (dispatched from (main)/layout.tsx onNew) fires this so we don't
  // have to lift the modal's state up to the layout.
  useEffect(() => {
    const handler = () => setManageOpen(true);
    window.addEventListener("triagent:open-manage-repos", handler);
    return () =>
      window.removeEventListener("triagent:open-manage-repos", handler);
  }, []);

  return (
    <div className="border-t border-zinc-800">
      {/* Header strip is the only chrome the panel takes when collapsed.
          The whole row toggles expansion; the inner "manage" target
          stops propagation so it opens the modal without flipping the
          collapse state. */}
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        aria-expanded={expanded}
        className="flex w-full items-center justify-between gap-2 px-3 py-2.5 text-left transition hover:bg-zinc-900/40"
      >
        <span className="flex items-baseline gap-1.5 text-xs uppercase tracking-wide text-zinc-500">
          <span aria-hidden className="inline-block w-2 text-zinc-600">
            {expanded ? "▾" : "▴"}
          </span>
          linked github projects
        </span>
        <span
          role="button"
          tabIndex={0}
          onClick={(e) => {
            e.stopPropagation();
            setManageOpen(true);
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter" || e.key === " ") {
              e.preventDefault();
              e.stopPropagation();
              setManageOpen(true);
            }
          }}
          className="rounded border border-zinc-800 px-1.5 py-0.5 text-xs text-zinc-400 transition hover:border-zinc-600 hover:text-zinc-200"
        >
          manage
        </span>
      </button>

      {expanded && (
        <div className="max-h-64 overflow-y-auto px-3 pb-3">
          {investigation ? (
            <>
              <p className="mb-1 px-1 text-xs text-zinc-600">
                linked to this investigation:
              </p>
              <ActiveReposList linkedRepos={investigation.linkedRepos ?? []} />
            </>
          ) : (
            <PendingReposList refreshNonce={refreshNonce} />
          )}
        </div>
      )}

      {manageOpen && (
        <ManageReposModal
          onClose={() => setManageOpen(false)}
          refreshNonce={refreshNonce}
        />
      )}
    </div>
  );
}
