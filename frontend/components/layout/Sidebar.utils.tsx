"use client";

import { type SyncState } from "@/lib/api";
import { CheckIcon, UnsyncedIcon } from "@/components/shared/Icons";

export type SidebarView = "investigations" | "watches" | "playbooks" | "mcp" | "wiki" | "repos";

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
export function SidebarSyncIcon({ syncState }: { syncState: SyncState }) {
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

export function formatRelative(iso: string): string {
  const d = new Date(iso);
  const diffSec = (Date.now() - d.getTime()) / 1000;
  if (diffSec < 60) return "just now";
  if (diffSec < 3600) return `${Math.floor(diffSec / 60)}m ago`;
  if (diffSec < 86400) return `${Math.floor(diffSec / 3600)}h ago`;
  if (diffSec < 86400 * 7) return `${Math.floor(diffSec / 86400)}d ago`;
  return d.toLocaleDateString();
}

export function sidebarViewFromPath(pathname: string): SidebarView {
  if (pathname.startsWith("/wiki")) return "wiki";
  if (pathname.startsWith("/watches")) return "watches";
  if (pathname.startsWith("/playbooks")) return "playbooks";
  if (pathname.startsWith("/repos")) return "repos";
  if (pathname.startsWith("/mcp")) return "mcp";
  if (pathname.startsWith("/investigations")) return "investigations";
  return "investigations";
}
