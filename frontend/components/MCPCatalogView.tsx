"use client";

import { useEffect, useMemo, useState } from "react";
import { api, ApiError, type ToolEntry } from "@/lib/api";
import {
  chipClasses,
  groupTools,
  serverDisplayLabel,
  type MCPCategory,
} from "@/lib/mcps";
import { Spinner } from "./Spinner";

type Props = {
  // Optional server alias to scroll-focus on first render. Set when the
  // user arrived via a deep link / ?server=… URL param. Optional and
  // pass-through — the catalog renders the same in either case.
  focusServer?: string | null;
  // Optional callback invoked when the user toggles a server section.
  // The route owns URL state and uses this to push `?server=<alias>`
  // (or clear the query when the user collapses the focused section).
  // When omitted, ServerSection falls back to local open/closed state —
  // preserving the legacy uncontrolled behaviour for any non-routed mount.
  onFocusServer?: (name: string | null) => void;
};

// MCPCatalogView is a standalone reference page. It works without an
// active investigation: the catalog is global (every kind triagent-mcp can
// serve), so operators can browse tool surfaces before kicking off any
// session. Source of truth is the catalog the launcher aggregates from
// each MCP's ToolSpecs() in-process at startup; this component just
// fetches it via /api/tools.
// Cap on server sections per page. Picked to keep the catalog scannable
// without forcing scroll — each section is collapsed by default but its
// header still occupies meaningful vertical space, and the parent <main>
// is intentionally non-scrolling so the operator sees the pager.
const MCP_PAGE_SIZE = 6;

export function MCPCatalogView({ focusServer, onFocusServer }: Props) {
  const [tools, setTools] = useState<ToolEntry[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [filter, setFilter] = useState("");
  const [page, setPage] = useState(0);

  useEffect(() => {
    let cancelled = false;
    api
      .listTools()
      .then((list) => {
        if (cancelled) return;
        setTools(list);
        setErr(null);
      })
      .catch((e) => {
        if (cancelled) return;
        setErr(e instanceof ApiError ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Scroll the focused server into view once the data lands.
  useEffect(() => {
    if (!focusServer || tools === null) return;
    const el = document.getElementById(`mcp-${focusServer}`);
    if (el) el.scrollIntoView({ behavior: "smooth", block: "start" });
  }, [focusServer, tools]);

  // Jump back to page 0 whenever the filter changes — without it the
  // pager can strand the operator on page N showing zero matches.
  useEffect(() => {
    setPage(0);
  }, [filter]);

  const grouped = useMemo(() => {
    if (!tools) return null;
    const matchesFilter = (t: ToolEntry) => {
      if (!filter.trim()) return true;
      const q = filter.toLowerCase();
      return (
        t.name.toLowerCase().includes(q) ||
        t.description.toLowerCase().includes(q) ||
        t.server.toLowerCase().includes(q)
      );
    };
    const filtered = tools.filter(matchesFilter);
    return groupTools(filtered);
  }, [tools, filter]);

  return (
    <div className="mx-auto max-w-4xl px-6 py-10">
      <header className="mb-6 space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight text-zinc-100">
          MCP tool catalog
        </h1>
        <p className="text-sm text-zinc-400">
          Every tool the triagent-mcp servers expose to claude during an
          investigation. Each kind (k8s, strategies, prom, git) ships a
          fixed surface; per-instance variations (e.g. one git server per
          linked repo) reuse the same tool set, parameterised by the
          server's startup flags.
        </p>
      </header>

      <div className="mb-4">
        <input
          type="search"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="filter by tool name, server, or description…"
          className="w-full rounded border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-100 placeholder-zinc-600 focus:border-zinc-600 focus:outline-none"
        />
      </div>

      {tools === null && !err && (
        <div className="flex items-center gap-2 text-sm text-zinc-500">
          <Spinner className="h-4 w-4" /> loading catalog…
        </div>
      )}

      {err && (
        <div className="rounded border border-red-900/60 bg-red-950/40 p-3 text-sm text-red-200/90">
          {err}
        </div>
      )}

      {tools !== null && tools.length === 0 && (
        <div className="rounded border border-zinc-800 bg-zinc-900/40 p-4 text-sm text-zinc-400">
          The launcher couldn't build the in-process tool catalog at startup.
          Rebuild the binary (<code className="font-mono">make build</code>),
          restart the launcher, and reload this page.
        </div>
      )}

      {grouped && grouped.size === 0 && tools && tools.length > 0 && (
        <div className="rounded border border-zinc-800 bg-zinc-900/40 p-4 text-sm text-zinc-400">
          No tools match <span className="font-mono">{filter}</span>.
        </div>
      )}

      {grouped && grouped.size > 0 && (() => {
        const entries = Array.from(grouped.entries());
        // If the focused server falls outside the current page, jump to
        // its page so deep-links land on a visible section instead of
        // silently scrolling into an off-page row.
        const focusedIdx = focusServer
          ? entries.findIndex(([s]) => s === focusServer)
          : -1;
        const totalPages = Math.max(
          1,
          Math.ceil(entries.length / MCP_PAGE_SIZE),
        );
        const targetPage =
          focusedIdx >= 0
            ? Math.floor(focusedIdx / MCP_PAGE_SIZE)
            : Math.min(page, totalPages - 1);
        const slice = entries.slice(
          targetPage * MCP_PAGE_SIZE,
          (targetPage + 1) * MCP_PAGE_SIZE,
        );
        return (
          <>
            <div className="space-y-6">
              {slice.map(([server, list]) => (
                <ServerSection
                  key={server}
                  server={server}
                  tools={list}
                  focused={focusServer === server}
                  forceOpen={filter.trim() !== ""}
                  onFocusServer={onFocusServer}
                />
              ))}
            </div>
            <Pager
              page={targetPage}
              totalPages={totalPages}
              onPrev={() => setPage((p) => Math.max(0, p - 1))}
              onNext={() =>
                setPage((p) => Math.min(totalPages - 1, p + 1))
              }
            />
          </>
        );
      })()}
    </div>
  );
}

function Pager({
  page,
  totalPages,
  onPrev,
  onNext,
}: {
  page: number;
  totalPages: number;
  onPrev: () => void;
  onNext: () => void;
}) {
  if (totalPages <= 1) return null;
  return (
    <div className="mt-4 flex items-center justify-end gap-2 text-xs text-zinc-500">
      <button
        type="button"
        onClick={onPrev}
        disabled={page === 0}
        className="rounded border border-zinc-800 px-2 py-0.5 transition hover:border-zinc-600 hover:text-zinc-200 disabled:cursor-not-allowed disabled:opacity-40"
      >
        ‹ prev
      </button>
      <span aria-live="polite">
        page {page + 1} of {totalPages}
      </span>
      <button
        type="button"
        onClick={onNext}
        disabled={page >= totalPages - 1}
        className="rounded border border-zinc-800 px-2 py-0.5 transition hover:border-zinc-600 hover:text-zinc-200 disabled:cursor-not-allowed disabled:opacity-40"
      >
        next ›
      </button>
    </div>
  );
}

function ServerSection({
  server,
  tools,
  focused,
  forceOpen,
  onFocusServer,
}: {
  server: string;
  tools: ToolEntry[];
  focused: boolean;
  // forceOpen overrides the user's collapse state — used while a search
  // filter is active so matching sections always reveal their tools.
  // Local `open` state is preserved underneath so clearing the filter
  // returns each section to whatever the user had set.
  forceOpen: boolean;
  // When provided, the parent route owns the focus/URL state. Clicks on
  // the section header notify the parent (toggle: focus this server, or
  // clear focus if it was already focused) and the section's open state
  // is driven entirely by `focused`. When omitted, falls back to local
  // uncontrolled open/closed state.
  onFocusServer?: (name: string | null) => void;
}) {
  const category = categoryFor(server);
  // Collapsed by default — the catalog can list a dozen+ servers and
  // each one expanded blew the page out. Deep-linked (?server=…) ones
  // start open so the user lands on what they asked for.
  const [open, setOpen] = useState(focused);
  // If focusServer changes (or arrives after data loads), open that section.
  useEffect(() => {
    if (focused) setOpen(true);
  }, [focused]);
  // In controlled mode (onFocusServer provided), `focused` is the source
  // of truth and the local `open` state is irrelevant for visibility.
  const controlled = !!onFocusServer;
  const effectiveOpen = controlled ? focused || forceOpen : open || forceOpen;
  const handleToggle = () => {
    if (onFocusServer) {
      onFocusServer(focused ? null : server);
    } else {
      setOpen((o) => !o);
    }
  };
  return (
    <section
      id={`mcp-${server}`}
      className={
        "rounded border bg-zinc-900/30 transition " +
        (focused ? "border-sky-700/60 ring-1 ring-sky-700/40" : "border-zinc-800")
      }
    >
      <button
        type="button"
        onClick={handleToggle}
        aria-expanded={effectiveOpen}
        className={
          "flex w-full flex-wrap items-baseline justify-between gap-2 px-4 py-3 text-left " +
          (effectiveOpen ? "border-b border-zinc-800" : "")
        }
      >
        <div className="space-y-0.5">
          <div className="flex items-baseline gap-2">
            <svg
              viewBox="0 0 12 12"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.5"
              strokeLinecap="round"
              strokeLinejoin="round"
              className={
                "inline-block h-3 w-3 shrink-0 self-center text-zinc-500 transition-transform " +
                (effectiveOpen ? "rotate-90" : "")
              }
              aria-hidden
            >
              <path d="M4.5 3l3 3-3 3" />
            </svg>
            <span
              className={
                "rounded border px-1.5 py-0.5 font-mono text-xs " +
                chipClasses(category)
              }
            >
              {server}
            </span>
            <span className="text-sm font-medium text-zinc-200">
              {serverDisplayLabel(server)}
            </span>
          </div>
          <p className="pl-5 text-xs text-zinc-500">
            {tools.length} tool{tools.length === 1 ? "" : "s"}
            {server === "triagent-git" && (
              <>
                {" · "}one running triagent-mcp instance per linked repo (same
                tool surface, different repo)
              </>
            )}
          </p>
        </div>
      </button>
      {effectiveOpen && (
        <ul className="divide-y divide-zinc-800/60">
          {tools.map((t) => (
            <ToolRow key={`${t.server}/${t.name}`} tool={t} />
          ))}
        </ul>
      )}
    </section>
  );
}

// ToolRow renders one entry. The header is always visible; clicking
// it toggles a collapsible body that lists the tool's inputs (with
// the description, type, and required marker pulled from each
// field's jsonschema tag). Tools with no inputs render a flat header
// without an expand affordance — keeps the no-input case quiet.
function ToolRow({ tool }: { tool: ToolEntry }) {
  const [open, setOpen] = useState(false);
  const inputs = tool.inputs ?? [];
  const hasInputs = inputs.length > 0;
  return (
    <li className="px-4 py-3">
      <button
        type="button"
        onClick={() => hasInputs && setOpen((o) => !o)}
        disabled={!hasInputs}
        aria-expanded={open}
        className={
          "w-full text-left " +
          (hasInputs ? "cursor-pointer" : "cursor-default")
        }
      >
        <div className="flex items-baseline gap-2">
          {hasInputs && (
            // Inline SVG chevron — rotates 90° when open. Replaces
            // the unicode ▶ glyph, which got coloured by the OS
            // emoji-font fallback on some platforms (came out as
            // a bright orange wedge against the zinc UI). Vector
            // ensures the colour is whatever currentColor says.
            <svg
              viewBox="0 0 12 12"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.5"
              strokeLinecap="round"
              strokeLinejoin="round"
              className={
                "inline-block h-3 w-3 shrink-0 self-center text-zinc-500 transition-transform " +
                (open ? "rotate-90" : "")
              }
              aria-hidden
            >
              <path d="M4.5 3l3 3-3 3" />
            </svg>
          )}
          <span className="font-mono text-sm text-sky-300">{tool.name}</span>
          <span className="text-xs text-zinc-600">
            mcp__{tool.server}__{tool.name}
          </span>
          {hasInputs && (
            <span className="ml-auto text-xs text-zinc-600">
              {inputs.length} input{inputs.length === 1 ? "" : "s"}
            </span>
          )}
        </div>
        <p className="mt-1 text-xs leading-relaxed text-zinc-400">
          {tool.description}
        </p>
      </button>
      {open && hasInputs && (
        <ul className="mt-3 space-y-2 rounded border border-zinc-800 bg-zinc-950/40 px-3 py-2">
          {inputs.map((inp) => (
            <li key={inp.name} className="text-xs">
              <div className="flex items-baseline gap-2">
                <span className="font-mono text-zinc-200">{inp.name}</span>
                <span className="font-mono text-xs text-zinc-500">
                  {inp.type}
                </span>
                {inp.required && (
                  <span className="rounded bg-amber-900/40 px-1.5 py-0.5 text-xs font-medium uppercase tracking-wide text-amber-300">
                    required
                  </span>
                )}
              </div>
              {inp.description && (
                <p className="mt-0.5 text-xs leading-relaxed text-zinc-400">
                  {inp.description}
                </p>
              )}
            </li>
          ))}
        </ul>
      )}
    </li>
  );
}

// categoryFor returns the chip color category for a logical server alias.
// Mirrors activeMCPs but operates on the catalog (which uses logical
// aliases) instead of the per-investigation wire aliases.
function categoryFor(server: string): MCPCategory {
  if (server === "triagent-prom") return "metrics";
  if (server === "triagent-git") return "git";
  if (server === "triagent-wiki") return "wiki";
  if (server === "triagent-slack") return "slack";
  if (server === "triagent-incidentio") return "incidentio";
  if (server === "triagent-k8s" || server === "triagent-strategies") return "core";
  return "docs"; // example-docs and any other detected docs MCP
}
