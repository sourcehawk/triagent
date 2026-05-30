// Helpers around the per-investigation MCP server set. The wire info is
// already on the Investigation DTO (promEnabled / docsPrefix / linkedRepos);
// this module just turns it into a renderable list and sorts the entries.

import type { Investigation, MCPCallStats, MCPProbeResult, ToolEntry } from "./api";

export type MCPCategory = "core" | "metrics" | "docs" | "git" | "wiki" | "slack" | "incidentio";

export type ActiveMCP = {
  alias: string; // wire alias (matches mcp__<alias>__<tool> in tool names)
  category: MCPCategory;
  description: string;
};

// activeMCPs derives the list of MCP servers wired into an investigation
// from its DTO. Order is stable and groups the categories (core first,
// then optional metrics/docs, then per-repo git servers in their resolved
// order) so the status bar reads the same way every time.
export function activeMCPs(inv: Investigation): ActiveMCP[] {
  const out: ActiveMCP[] = [
    {
      alias: "triagent-k8s",
      category: "core",
      description:
        "Read-only Kubernetes — list_resources, get_resource, get_logs, list_events, trace_crossplane. Scoped to this namespace.",
    },
    {
      alias: "triagent-strategies",
      category: "core",
      description:
        "Investigation walker — list_playbooks, walk_playbook, step_complete, summarize.",
    },
    {
      alias: "triagent-meta",
      category: "core",
      description:
        "Launcher metadata — set_session_label (one-line summary shown in the sidebar history list).",
    },
    {
      alias: "triagent-parallel",
      category: "core",
      description:
        "Dispatch broker — call (fan-out 2..8 allowlisted sub-agent calls in parallel: analyze_change, correlate_with_findings, propose_wiki_draft, propose_playbook_draft).",
    },
  ];

  if (inv.promEnabled) {
    out.push({
      alias: "triagent-prom",
      category: "metrics",
      description:
        "Curated Prometheus signals — saturation (cpu/memory/storage/jvm/restart/gateway) plus per-app bundles.",
    });
  }

  if (inv.docsPrefix) {
    // docsPrefix arrives as `mcp__<alias>__`; strip the wrapper for display.
    const alias = inv.docsPrefix.replace(/^mcp__/, "").replace(/__$/, "");
    out.push({
      alias,
      category: "docs",
      description: "Documentation search.",
    });
  }

  for (const r of inv.linkedRepos ?? []) {
    const repoAlias = r.alias ?? r.name;
    const desc = r.description
      ? `${r.owner}/${r.name} — ${r.description}`
      : `${r.owner}/${r.name}`;
    out.push({
      alias: `triagent-git-${repoAlias}`,
      category: "git",
      description: desc,
    });
  }

  if (inv.slackMCPEnabled) {
    out.push({
      alias: "triagent-slack",
      category: "slack",
      description:
        "Slack — channel/thread search, summarisation, and message resolution against the linked workspace.",
    });
  }

  if (inv.incidentioMCPEnabled) {
    out.push({
      alias: "triagent-incidentio",
      category: "incidentio",
      description: "incident.io — incident timeline, updates, and metadata.",
    });
  }

  return out;
}

// chipClasses returns the Tailwind className string for a category's chip
// styling. Kept here so the status bar and any future surface (e.g. the
// activity-panel filter chips, if we ever color-code them too) agree.
export function chipClasses(c: MCPCategory): string {
  switch (c) {
    case "core":
      return "border-zinc-700 bg-zinc-900/60 text-zinc-300";
    case "metrics":
      return "border-sky-900/60 bg-sky-950/40 text-sky-300";
    case "docs":
      return "border-amber-900/60 bg-amber-950/40 text-amber-300";
    case "git":
      return "border-emerald-900/60 bg-emerald-950/40 text-emerald-300";
    case "wiki":
      return "border-violet-900/60 bg-violet-950/40 text-violet-300";
    case "slack":
      return "border-pink-500/70 bg-pink-500/20 text-pink-300";
    case "incidentio":
      return "border-rose-900/60 bg-rose-950/40 text-rose-300";
  }
}

// logicalServer maps a wire alias (the form used in mcp.json + the
// MCPStatusBar chips) to the LOGICAL kind alias used by the launcher's
// tool catalog. Per-repo git instances all collapse to the single
// `triagent-git` catalog entry — the tool surface is identical regardless of
// which repo a given instance points at.
export function logicalServer(wireAlias: string): string {
  if (wireAlias.startsWith("triagent-git-")) return "triagent-git";
  return wireAlias;
}

// groupTools buckets the flat tool list by server alias. Returns a Map
// so callers can iterate insertion-order-preserving.
export function groupTools(tools: ToolEntry[]): Map<string, ToolEntry[]> {
  const out = new Map<string, ToolEntry[]>();
  for (const t of tools) {
    const list = out.get(t.server);
    if (list) list.push(t);
    else out.set(t.server, [t]);
  }
  return out;
}

// serverDisplayLabel returns a human-friendly title for a logical server
// alias used in the catalog drawer's section headings.
export function serverDisplayLabel(server: string): string {
  switch (server) {
    case "triagent-k8s":
      return "Kubernetes (read-only)";
    case "triagent-strategies":
      return "Investigation playbooks";
    case "triagent-meta":
      return "Session metadata";
    case "triagent-parallel":
      return "Parallel dispatch broker";
    case "triagent-prom":
      return "Prometheus signals";
    case "triagent-git":
      return "GitHub repository (per-repo instance)";
    case "triagent-wiki":
      return "Investigations wiki";
    default:
      // Docs MCPs come through with whatever alias the launcher
      // detected (e.g. "example-docs"); show as-is.
      return server;
  }
}

// instancesOfServer returns the wire aliases of every active MCP that
// maps to the given logical server. Used by the catalog drawer to show
// "wired in this session as: triagent-git-example, triagent-git-host, …" for
// per-repo kinds.
export function instancesOfServer(
  inv: Investigation,
  logical: string,
): string[] {
  const all = activeMCPs(inv);
  return all.filter((m) => logicalServer(m.alias) === logical).map((m) => m.alias);
}

// ── MCP health classification ────────────────────────────────────────────────

export type MCPHealthClass = "never-seen" | "healthy" | "stale" | "degraded" | "probe-failed";

// classifyMCPHealth combines passive telemetry stats with an optional active
// probe result. probe-failed dominates: a broken dependency makes the MCP
// unusable regardless of recent call history.
export function classifyMCPHealth(
  stats: MCPCallStats | undefined,
  probe?: MCPProbeResult,
): MCPHealthClass {
  // Probe failure dominates everything. The MCP's dependency is broken
  // right now, regardless of what the telemetry says.
  if (probe && !probe.ok) return "probe-failed";

  if (!stats || stats.calls === 0) return "never-seen";
  const now = Date.now();
  const lastSeen = stats.lastSeen ? Date.parse(stats.lastSeen) : 0;
  const lastErr = stats.lastErrorAt ? Date.parse(stats.lastErrorAt) : 0;
  if (lastErr && now - lastErr < 30_000) return "degraded";
  if (lastSeen && now - lastSeen < 60_000) return "healthy";
  return "stale";
}

// healthClasses composes chipClasses(category) with health-driven modifiers.
// probe-failed and degraded override colour entirely; stale/never-seen reduce opacity.
export function healthClasses(
  c: MCPCategory,
  stats: MCPCallStats | undefined,
  probe?: MCPProbeResult,
): string {
  const cls = classifyMCPHealth(stats, probe);
  const base = chipClasses(c);
  switch (cls) {
    case "probe-failed":
      return "border-red-700 bg-red-950/40 text-red-300";
    case "degraded":
      return "border-red-700 bg-red-950/40 text-red-300";
    case "stale":
      return base + " opacity-60";
    case "never-seen":
      return base + " opacity-50";
    case "healthy":
      return base;
  }
}

// formatAgo returns a compact human-readable relative time string.
// e.g. "3s ago", "12m ago", "2h ago".
export function formatAgo(date: Date | string): string {
  const d = typeof date === "string" ? new Date(date) : date;
  const ms = Date.now() - d.getTime();
  if (ms < 60_000) return `${Math.round(ms / 1000)}s ago`;
  if (ms < 3_600_000) return `${Math.round(ms / 60_000)}m ago`;
  return `${Math.round(ms / 3_600_000)}h ago`;
}

// healthTooltip returns a two-line tooltip string for a chip.
// Line 1: the server description.
// Line 2: status summary (probe state + telemetry).
export function healthTooltip(
  m: ActiveMCP,
  stats: MCPCallStats | undefined,
  probe?: MCPProbeResult,
): string {
  const cls = classifyMCPHealth(stats, probe);
  let statusLine: string;
  switch (cls) {
    case "probe-failed": {
      const reason = probe?.reason ?? "dependency unreachable";
      statusLine = `Probe failed: ${reason}`;
      break;
    }
    case "never-seen": {
      statusLine = "registered, no calls yet";
      if (probe?.ok) statusLine += ` · dep ok (${probe.latencyMs}ms)`;
      break;
    }
    case "healthy":
    case "stale": {
      const parts: string[] = [`${stats!.calls} call${stats!.calls === 1 ? "" : "s"}`];
      if (stats!.errors > 0) parts.push(`${stats!.errors} error${stats!.errors === 1 ? "" : "s"}`);
      if (stats!.lastSeen) parts.push(`last seen ${formatAgo(stats!.lastSeen)}`);
      statusLine = parts.join(", ");
      if (probe?.ok) statusLine += ` · dep ok (${probe.latencyMs}ms)`;
      break;
    }
    case "degraded": {
      const ago = stats!.lastErrorAt ? formatAgo(stats!.lastErrorAt) : "";
      const msg = stats!.lastErrorMsg
        ? stats!.lastErrorMsg.slice(0, 120) + (stats!.lastErrorMsg.length > 120 ? "…" : "")
        : "tool error";
      statusLine = `last error ${ago}: ${msg}`;
      if (probe?.ok) statusLine += ` · dep ok (${probe.latencyMs}ms)`;
      break;
    }
  }
  return `${m.description}\n${statusLine}`;
}
