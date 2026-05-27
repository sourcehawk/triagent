// SyncState mirrors the Go shape in internal/server/sync_state.go.
//
// The status enum is uniform across resource kinds (playbooks today;
// sessions and wiki next stage) so frontend rendering doesn't have to
// switch on kind for the basic synced/drifted/missing distinction. The
// per-kind UI decides what affordance to show — e.g. "upstream-only"
// for playbooks is normal (operator using upstream as-is, no badge);
// for sessions it's a call-to-action ("import to adopt locally").
//
// If you change the status enum or shape here, change Go side too.

export type SyncStatus =
  | "synced"
  | "local-drift"
  | "local-only"
  | "upstream-only"
  | "closed"
  | "unknown";

export type SyncState = {
  status: SyncStatus;
  // Reason is a human-readable explanation; UI uses it for tooltip
  // text. Not load-bearing for rendering decisions — those switch on
  // status.
  reason?: string;
  // PR is populated for resources that flow through a pull request
  // (sessions today). Lets a card render a state-coloured pill +
  // relative-time tooltip without recomputing the same data from
  // the resource's own PR fields. Absent for kinds that don't track
  // PRs per-id (playbooks).
  pr?: SyncStatePR;
};

export type SyncStatePR = {
  url: string;
  state: "open" | "merged" | "closed";
  // RFC3339 timestamps from the launcher; frontend formats them
  // relative-to-now via the existing relativeTime helper.
  mergedAt?: string;
  closedAt?: string;
};
