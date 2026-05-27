"use client";

import { useEffect, useState } from "react";
import type { Investigation, MCPCallStats, MCPProbeResult } from "@/lib/api";
import { api } from "@/lib/api";
import { activeMCPs, classifyMCPHealth, formatAgo, healthClasses, healthTooltip } from "@/lib/mcps";

type Props = {
  investigation: Investigation;
};

// MCPStatusBar surfaces every MCP server wired into the current
// investigation, coloured by live health. Two independent polls run
// concurrently:
//   - Telemetry (5s): passive stats from tool-call recording.
//   - Probe (30s): active readiness checks against each MCP's dependency.
//
// Probe-failed overrides chip colour regardless of telemetry, so a broken
// Prometheus URL shows red even if no tool calls have been made yet.
//
// Chips start in the "never-seen" muted state and light up as the agent
// fires tool calls or the probe confirms the dependency is healthy.
export function MCPStatusBar({ investigation }: Props) {
  const mcps = activeMCPs(investigation);
  const [health, setHealth] = useState<Record<string, MCPCallStats>>({});
  const [probes, setProbes] = useState<Record<string, MCPProbeResult>>({});
  const [lastProbeAt, setLastProbeAt] = useState<Date | undefined>();

  // Existing telemetry poll (5s) — unchanged.
  useEffect(() => {
    if (investigation.archived) return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;

    async function poll() {
      try {
        const res = await api.getMCPHealth(investigation.id);
        if (cancelled) return;
        const map: Record<string, MCPCallStats> = {};
        for (const s of res.servers) map[s.alias] = s;
        setHealth(map);
      } catch {
        // best-effort; surface staleness via the existing classification
      }
      if (!cancelled) timer = setTimeout(poll, 5000);
    }
    poll();
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [investigation.id, investigation.archived]);

  // Probe poll (30s). Deferred 1s on initial mount so the kubectl
  // round-trip (~500ms-1.3s of server-side wait) doesn't compete with
  // /transcript and Next.js RSC prefetches for connection slots
  // during the critical first second of a session view mount. The MCP
  // status bar's contents aren't on the user's critical path — a 1s
  // delay before "active mcp" populates is fine; locking the
  // transcript fetch behind a kubectl probe is not.
  useEffect(() => {
    if (investigation.archived) return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;

    async function poll() {
      try {
        const res = await api.getMCPProbe(investigation.id);
        if (cancelled) return;
        const map: Record<string, MCPProbeResult> = {};
        for (const s of res.servers) map[s.alias] = s;
        setProbes(map);
        setLastProbeAt(new Date());
      } catch {
        // keep last known probe state
      }
      if (!cancelled) timer = setTimeout(poll, 30_000);
    }
    timer = setTimeout(poll, 1000);
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [investigation.id, investigation.archived]);

  if (investigation.archived) return null;

  return (
    <div className="px-1">
      <div className="mb-1 flex items-baseline justify-between">
        <h3 className="text-sm font-medium text-zinc-200">active mcp</h3>
        <span className="text-xs text-zinc-500">
          {mcps.length} server{mcps.length === 1 ? "" : "s"}
          {lastProbeAt && (
            <span className="ml-1 text-zinc-600">
              · checked {formatAgo(lastProbeAt)}
            </span>
          )}
        </span>
      </div>
      <div className="flex flex-wrap gap-1">
        {mcps.map((m) => {
          const stats = health[m.alias];
          const probe = probes[m.alias];
          const cls = healthClasses(m.category, stats, probe);
          const tooltip = healthTooltip(m, stats, probe);
          const healthClass = classifyMCPHealth(stats, probe);
          return (
            <span
              key={m.alias}
              title={tooltip}
              className={
                "rounded border px-1.5 py-0.5 font-mono text-xs inline-flex items-center " +
                cls +
                (healthClass === "degraded" || healthClass === "probe-failed" || (stats?.inFlight ?? 0) > 0 ? " animate-pulse" : "")
              }
            >
              {m.alias}
              {stats && stats.inFlight > 0 && (
                <span
                  className="ml-1 inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-current"
                  aria-label="in-flight"
                />
              )}
            </span>
          );
        })}
      </div>
    </div>
  );
}
