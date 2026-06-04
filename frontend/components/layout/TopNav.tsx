"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

// TopNav is the global navigation bar at the top of every page. The
// active tab is derived from the URL — no view prop needed. Tabs use
// next/link so navigation between (main)-group routes is a SPA soft-nav
// that preserves the layout's <StreamProvider> (and its single
// EventSource). A bare <a> here would full-reload, remounting the
// provider per click and saturating Chrome's per-origin connection
// pool with closing SSE sockets after a few clicks.
export function TopNav() {
  const pathname = usePathname() ?? "/";
  const active = activeTab(pathname);

  return (
    <header className="flex shrink-0 items-center gap-6 border-b border-zinc-800 bg-zinc-950 px-4 py-2">
      <Brand />
      <nav className="flex items-stretch gap-1">
        {/* Order: investigate → watches → mcp → playbooks → repos → wiki → docs.
            Repos sit next to wiki because both are persistent
            knowledge surfaces the agent reads as orientation. */}
        <NavTab label="investigate" href="/" active={active === "investigations"} />
        <NavTab label="watches" href="/watches" active={active === "watches"} />
        <NavTab label="mcp" href="/mcp" active={active === "mcp"} />
        <NavTab label="playbooks" href="/playbooks" active={active === "playbooks"} />
        <NavTab label="repos" href="/repos" active={active === "repos"} />
        <NavTab label="wiki" href="/wiki" active={active === "wiki"} />
        <NavTab label="docs" href="/docs" active={active === "docs"} />
      </nav>
    </header>
  );
}

// activeTab maps the current pathname to the tab to highlight.
// /investigations/<id> still highlights "investigate"; /repos/<owner>/<name>
// still highlights "repos".
function activeTab(
  pathname: string,
): "investigations" | "watches" | "mcp" | "playbooks" | "repos" | "wiki" | "docs" {
  if (pathname.startsWith("/wiki")) return "wiki";
  if (pathname.startsWith("/watches")) return "watches";
  if (pathname.startsWith("/playbooks")) return "playbooks";
  if (pathname.startsWith("/repos")) return "repos";
  if (pathname.startsWith("/mcp")) return "mcp";
  if (pathname.startsWith("/docs")) return "docs";
  if (pathname.startsWith("/investigations")) return "investigations";
  return "investigations";
}

function Brand() {
  return (
    <div className="flex items-baseline gap-2">
      <span className="text-sm font-semibold tracking-tight text-zinc-100">
        Triagent
      </span>
      <span className="rounded border border-amber-700/60 bg-amber-900/30 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-amber-300">
        alpha
      </span>
      <span className="hidden text-xs text-zinc-500 sm:inline">
        Agentic Incident Investigation
      </span>
    </div>
  );
}

function NavTab({
  label,
  href,
  active,
}: {
  label: string;
  href: string;
  active: boolean;
}) {
  return (
    <Link
      href={href}
      aria-current={active ? "page" : undefined}
      className={
        "-mb-px relative px-3 py-2 text-xs font-medium uppercase tracking-wide transition " +
        (active
          ? "border-b-2 border-zinc-100 text-zinc-100"
          : "border-b-2 border-transparent text-zinc-500 hover:text-zinc-300")
      }
    >
      {label}
    </Link>
  );
}
