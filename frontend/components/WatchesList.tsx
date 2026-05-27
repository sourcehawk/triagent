"use client";

import Link from "next/link";
import { useState } from "react";
import { watchesAPI, type Watch, type WatchSourceKind } from "@/lib/api";
import { WatchStatusPill } from "./WatchStatusPill";
import { GitHubIcon, SlackIcon } from "./Icons";
import { Spinner } from "./Spinner";

export function WatchesList({
  watches,
  onMutated,
}: {
  watches: Watch[] | null;
  // Called after a row-level mutation (enable/disable toggle) so the
  // parent's useWatches hook can refresh the list. SSE doesn't fire on
  // PATCH today, so we nudge manually.
  onMutated?: () => void;
}) {
  if (watches === null) {
    return <div className="text-sm text-zinc-500">Loading…</div>;
  }
  if (watches.length === 0) {
    return (
      <div className="rounded border border-zinc-800 bg-zinc-900/40 p-8 text-center">
        <h2 className="text-lg font-medium text-zinc-400">No watches yet</h2>
        <p className="mt-2 text-sm text-zinc-500">
          Watches poll GitHub issues or Slack channels every few minutes and (optionally) auto-start an investigation when something actionable shows up. Use <span className="text-zinc-300">+ new watch</span> in the sidebar to create one.
        </p>
      </div>
    );
  }
  return (
    <ul className="divide-y divide-zinc-800 rounded border border-zinc-800">
      {watches.map(w => (
        <li key={w.id} className="flex items-start justify-between gap-3 hover:bg-zinc-900/40">
          <Link href={`/watches/${w.id}`} className="flex flex-1 items-start gap-3 px-4 py-3">
            <SourceIcon kind={w.source.kind} className="mt-0.5 h-5 w-5 shrink-0 text-zinc-400" />
            <div className="min-w-0 flex-1">
              <div className="flex items-baseline gap-2">
                <span className="font-medium text-zinc-100">{w.name}</span>
                <span className="text-xs text-zinc-500">{sourceSummary(w)}</span>
              </div>
              {w.description && (
                <div className="mt-1 text-sm text-zinc-400">{w.description}</div>
              )}
            </div>
          </Link>
          <div className="flex shrink-0 items-center gap-3 px-4 py-3">
            {w.ingesting && (
              <span
                className="inline-flex items-center gap-1.5 rounded-full bg-amber-900/50 px-2 py-0.5 text-xs text-amber-300"
                title="Ingestion agent is running for this watch."
              >
                <Spinner className="h-3 w-3" />
                ingesting
              </span>
            )}
            {w.ingest?.enabled && (
              <span
                className="inline-flex items-center gap-1.5 rounded-full bg-violet-900/50 px-2 py-0.5 text-xs text-violet-300"
                title="Ingestion agent runs every poll to classify items into rich signals."
              >
                auto-ingest
              </span>
            )}
            {w.autoStart?.enabled && (
              <span
                className="inline-flex items-center gap-1.5 rounded-full bg-pink-900/50 px-2 py-0.5 text-xs text-pink-300"
                title={`Auto-spawns investigations from agent recommendations (max ${w.autoStart.maxConcurrent ?? 1} concurrent).`}
              >
                auto-start
              </span>
            )}
            <WatchStatusPill status={w.enabled ? "healthy" : "disabled"} />
            <EnableToggle watch={w} onMutated={onMutated} />
          </div>
        </li>
      ))}
    </ul>
  );
}

function EnableToggle({
  watch,
  onMutated,
}: {
  watch: Watch;
  onMutated?: () => void;
}) {
  const [busy, setBusy] = useState(false);
  return (
    <button
      type="button"
      disabled={busy}
      onClick={async (e) => {
        e.preventDefault();
        e.stopPropagation();
        setBusy(true);
        try {
          await watchesAPI.patch(watch.id, { enabled: !watch.enabled });
          onMutated?.();
        } finally {
          setBusy(false);
        }
      }}
      title={watch.enabled ? "Pause polling for this watch" : "Resume polling for this watch"}
      className="rounded border border-zinc-700 px-2 py-0.5 text-xs text-zinc-400 transition hover:border-zinc-500 hover:text-zinc-200 disabled:cursor-not-allowed disabled:opacity-50"
    >
      {watch.enabled ? "pause" : "resume"}
    </button>
  );
}

function SourceIcon({ kind, className }: { kind: WatchSourceKind; className?: string }) {
  if (kind === "slack_channel") return <SlackIcon className={className} />;
  return <GitHubIcon className={className} />;
}

function sourceSummary(w: Watch): string {
  if (w.source.kind === "github_issues") {
    const labels = w.source.labels?.length ? ` · labels: ${w.source.labels.join(", ")}` : "";
    return `${w.source.owner}/${w.source.repo}${labels}`;
  }
  return `#${w.source.channelName ?? w.source.channelID}`;
}
