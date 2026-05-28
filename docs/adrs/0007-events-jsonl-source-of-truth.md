# ADR-0007: `events.jsonl` as the source of truth for cluster/context history

- **Status**: Accepted
- **Date**: 2026-05-28

## Decision

`events.jsonl` is the source of truth for cluster/context history during an investigation. Don't persist `ContextName` / `ClusterID` on `Investigation`. Sessions can touch multiple clusters; the event log carries the full record.

Long-running work (push-PR, sub-agent generation) survives client disconnect by running on a goroutine with a context derived from the manager's parent, not from `r.Context()`. State transitions emit SSE so any open tab picks them up. Don't tie work lifetime to a request.

Persistent in-progress flags get an orphan-recovery sweep on launcher restart. Any `PushInProgress: true` etc. that survives a restart belongs to a goroutine that no longer exists — convert it to an explicit error state on `Restore()`, don't pretend it's still running.

Single-flight by `<owner>/<name>` (or equivalent) for any work the user can re-trigger (repo architecture summary, etc.). The worker keeps an in-process registry keyed by the natural id; a concurrent kick is a no-op, not a double-spend.

Wire callback seams for cross-package events (e.g. `Forward func(Event)` on `editor.Session`) rather than per-package subscriber pools. The launcher's Manager owns the single fan-out point.

## Context

A persisted `ContextName` / `ClusterID` would be a lie because sessions touch multiple clusters during their lifetime (via `switch_context`). The event log already records every context switch with its timestamp, so synthesizing "the cluster" from a snapshot field would silently drop history. Treat the log as the record; derive views from it.

Long-running work tied to `r.Context()` cancels when the client disconnects (tab close, sleep, network blip). The whole point of pushable work is that it survives that — so it lives on a manager-rooted goroutine and signals state changes via SSE.

Orphan-recovery exists because a launcher crash leaves "in progress" flags pointing at goroutines that no longer exist. Without recovery on `Restore()`, the UI shows "still working" forever; with recovery, the user sees the failure honestly and can retry.

Single-flight prevents double-spends on operator re-kicks. The natural id (e.g. `<owner>/<name>` for repo architecture summary) is the dedup key; an in-process registry is enough because the operations are one-launcher.

## Consequences

- New session state never persists derived properties (current cluster, current context). Compute from the event log.
- Long-running work never takes `r.Context()` as the work context; it takes a manager-rooted context.
- Every persistent in-progress flag has a paired orphan-recovery path in `Restore()`. Adding one without the other is a TODO that ages badly.
- Re-triggerable work uses an in-process single-flight registry keyed by the natural id.
- Cross-package event fan-out goes through Manager-owned seams; don't reinvent subscriber pools per package.
