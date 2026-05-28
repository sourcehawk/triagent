# ADR-0005: Single multiplexed SSE per browser tab via `<StreamProvider>`

- **Status**: Accepted
- **Date**: 2026-05-28

## Decision

One global `<StreamProvider>` owns a single multiplexed `EventSource` per browser tab. Per-component code subscribes via a filter API.

**Never open per-scope SSE connections.** Backlog is delivered via REST `/transcript` endpoints; the live stream uses seq-dedup.

DOM custom events use the `triagent:` prefix; localStorage keys use the `triagent.` prefix. Don't introduce any other namespace prefix for new events or storage keys.

## Context

Per-component `EventSource` looked tempting — each component would own its own stream and unsubscribe on unmount. But Chrome's HTTP/1.1 per-origin connection pool is 6 connections. Rapid sidebar navigation across many surfaces blew up the pool, queued the rest, and the UI froze.

A single multiplexed `EventSource` with a filter API is one connection per tab regardless of how many subscribers are active. Subscribers register and unregister cheaply; the pool stays unsaturated.

Backlog over REST avoids the dual-source problem (live stream + backlog buffer can deliver duplicates); seq-dedup on the live side closes the gap.

## Consequences

- Never reintroduce a per-component `EventSource`. The temptation is to "just open one for this surface" — it bursts the pool.
- New live subscriptions go through the `<StreamProvider>` filter API.
- Backlog is REST-shaped; live stream is delta-shaped with sequence numbers.
- DOM event and localStorage namespaces are `triagent:` and `triagent.` respectively; new prefixes need a strong reason.
