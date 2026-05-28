# ADR-0006: URL as the source of truth for view state

- **Status**: Accepted
- **Date**: 2026-05-28

## Decision

The URL is the source of truth for view state. Routing uses Next App Router + route groups (`app/(main)/`). Read state via `usePathname` / `useSearchParams`. Never reimplement URL state.

Editor session continuity uses query-param routing (`/playbooks?playbook=<id>`), not path segments — accepting a proposal from `__new` would otherwise unmount the chat. Rebinding the editor session's `Subject` happens server-side via `POST /api/editor-sessions/{id}/rebind` under the manager lock.

Shared chrome (`TopNav` + `Sidebar`) lives once in a route-group layout (`app/(main)/layout.tsx`). Wiki has its own layout under `app/wiki/layout.tsx` but uses the same `<TopNav>` / `<AppShell>` building blocks.

## Context

The earlier model used a `View` discriminator in `app/page.tsx` plus `readURLState` / `writeURLState` helpers. Two problems: the discriminator drifted from the URL (e.g. a wiki link in chat didn't change `View` until a navigation effect fired), and `writeURLState` raced with browser history.

Letting the URL be the only source of truth fixes both: Next's router is the single writer, `usePathname` / `useSearchParams` are the only readers, and the components below the route boundary re-render naturally on URL change.

Query-param continuity (over path segments) for editor sessions specifically: when an operator accepts a proposal from `__new`, the editor session rebinds to a real playbook id. If `playbook` were a path segment, the route boundary would unmount the chat. Keeping it a query param means the same React tree renders both states.

## Consequences

- Never reintroduce `View` / `readURLState` / `writeURLState`.
- Route groups (`app/(main)/`, `app/wiki/`) carry shared chrome; don't duplicate `TopNav` / `Sidebar` in pages.
- Editor session id stays in path; the editable subject id stays in query params.
- Server-side `Subject` rebinding happens under the manager lock to avoid two clients racing the same session id.
