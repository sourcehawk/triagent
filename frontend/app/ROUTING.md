# Frontend routing model

The launcher's web UI is a Next.js 16 App Router app under `app/`. Every top-level navbar view is its own route. Chrome
(top nav, sidebar) lives in shared layouts; pages render only their content.

## Route map

| URL                            | File                                                | What renders                                                      |
| ------------------------------ | --------------------------------------------------- | ----------------------------------------------------------------- |
| `/`                            | `app/(main)/page.tsx`                               | Upstream sessions browser (`UpstreamHome`) — pushed-session cards |
| `/investigations/new`          | `app/(main)/investigations/new/page.tsx`            | Cluster picker → form → preflight (start a fresh investigation)   |
| `/investigations/<id>`         | `app/(main)/investigations/[id]/{page,client}.tsx`  | Active investigation (SessionWorkspace)                           |
| `/sessions/<slug>`             | `app/(main)/sessions/[slug]/page.tsx`               | Read-only render of an upstream `session.md` + open-transcript    |
| `/playbooks`                   | `app/(main)/playbooks/page.tsx`                     | Playbook list (or editor when `?playbook=<id>` is present)        |
| `/mcp`                         | `app/(main)/mcp/page.tsx`                           | MCP catalog (focuses one server when `?server=<alias>` is present)|
| `/repos`                       | `app/(main)/repos/page.tsx`                         | Linked repos (renders one repo's architecture when `?repo=<owner>/<name>` is present) |
| `/docs`                        | `app/(main)/docs/page.tsx`                          | Docs landing (overview)                                           |
| `/docs/<section>`              | `app/(main)/docs/[section]/{page,client}.tsx`       | Docs section (overview / investigations / mcp / playbooks / wiki) |
| `/wiki`                        | `app/wiki/page.tsx`                                 | Wiki home (entry list + entity browser)                           |
| `/wiki/entries?slug=<slug>`    | `app/wiki/entries/{page,client}.tsx`                | Wiki entry editor                                                 |
| `/wiki/entities?type=<type>&name=<name>` | `app/wiki/entities/{page,client}.tsx`     | Wiki entity editor                                                |

## Layout layering

```
app/layout.tsx            — root: providers (DialogProvider). No chrome.
├── app/(main)/layout.tsx — (route group) AppShell + NewPlaybookModal + NewTypeModal mounts; "+ new" dispatcher
└── app/wiki/layout.tsx   — AppShell + NewWikiEntryModal mount
```

`(main)` is a Next.js **route group** — the parens make it a path-segment-free folder. Routes under it share the layout
but their URLs don't include `/main`.

## AppShell — the shared chrome

`components/AppShell.tsx` is the host for the global TopNav and global left Sidebar. Both layouts (`(main)` and `wiki`)
mount it. AppShell never mounts modals — modals are per-view and live in the layout that owns their state.

`<AppShell>` props:

- `activeId`, `onSelect` — sidebar selection plumbing
- `onNew`, `onNewType` — sidebar "+ new …" buttons
- `showSidebar` — set false on `/docs/*`; DocsView owns its own left rail
- `rightRail` — used today only conceptually; the wiki home renders its `<WikiSideNav>` as a sibling child of AppShell
  rather than via this prop

## How "+ new" dispatch works

The Sidebar emits `onNew()`. Each layout decides what that means by inspecting `usePathname()`:

- `(main)/layout.tsx`:
  - `/playbooks*` → opens `NewPlaybookModal`
  - `/mcp*` / `/docs*` → no-op
  - default (`/`, `/investigations/*`) → `router.push("/")` (returns to the upstream-sessions home; the actual fresh
    investigation lives at `/investigations/new`, which the sidebar's `+ new investigation` link goes to directly)
- `wiki/layout.tsx`: opens `NewWikiEntryModal`

The Sidebar suppresses the "+ new" block on `view === "mcp"` (no creation flow exists for the MCP catalog). On
`/docs/*`, the Sidebar isn't rendered at all — `(main)/layout.tsx` passes `showSidebar={false}`.

## How sidebar state is derived

`Sidebar` derives its `view` from the current pathname via the helper `sidebarViewFromPath`:

| pathname prefix   | SidebarView        |
| ----------------- | ------------------ |
| `/wiki`           | `"wiki"`           |
| `/playbooks`      | `"playbooks"`      |
| `/mcp`            | `"mcp"`            |
| `/investigations` | `"investigations"` |
| _fallback_        | `"investigations"` |

Note that `/docs` has no entry — Sidebar isn't rendered there. Callers may still pass an explicit `view` prop (tests /
one-off cases).

## How TopNav state is derived

`TopNav` derives its active tab from the current pathname via the helper `activeTab`. Same prefix mapping as the
sidebar, plus an explicit `/docs` branch (TopNav is rendered everywhere, including on `/docs/*`).

## Static export quirk: `[id]` placeholders

The Next.js static export pre-renders dynamic segments as `_` placeholder shells. `useParams()` returns `"_"` for the
placeholder regardless of the real URL — and doesn't re-render on intra-route navigation. Every `[param]/client.tsx`
works around this by reading the id from `usePathname()` with a regex:

```ts
const m = pathname.match(/\/wiki\/incidents\/([^/]+)\/?$/);
const id = m ? decodeURIComponent(m[1]) : "";
if (!id || id === "_") return <Loading />;
```

`page.tsx` is a thin server-component wrapper that calls `generateStaticParams() => [{ id: "_" }]` and renders the
client. Follow this split (page + client) for any new dynamic-segment route.

## URL conventions

- Use `router.push(\`/<route>/${encodeURIComponent(id)}\`)` to navigate to dynamic-segment routes.
- For views where the "focused item" is in-page state (the catalog still renders the list; the focus is just a scroll
  + open-section hint), use a query string instead: `router.push(\`/<route>?<key>=${encodeURIComponent(id)}\`)`. The
  `/playbooks`, `/mcp`, and `/repos` views follow this pattern. Migrating stale `/<route>/<id>` bookmarks to the query form lives
  in the page's `useEffect` (one `router.replace` on mount).
- Use `router.replace(...)` for redirects (e.g. legacy `?inv=` → `/investigations/<id>`).

## Adding a new top-level view

1. Add it under `app/(main)/<view>/page.tsx` (dynamic segment goes to `[param]/{page,client}.tsx`).
2. Add the prefix to `sidebarViewFromPath` (in `Sidebar.tsx`) — and to `activeTab` (in `TopNav.tsx`).
3. Add a tab to `TopNav`'s nav list.
4. If the view has a "+ new …" creation flow, mount the modal in `(main)/layout.tsx` and add a branch to its `onNew`
   dispatcher.
5. If the view shouldn't share the global Sidebar (like `/docs`), add a branch to the layout's `showSidebar` derivation.
