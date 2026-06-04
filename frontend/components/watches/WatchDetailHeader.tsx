"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { watchesAPI, type Watch, type WatchSourceKind } from "@/lib/api";
import { useDialog } from "@/lib/dialog";
import { ArrowLeftIcon, GitHubIcon, SlackIcon } from "@/components/shared/Icons";
import { WatchStatusPill } from "@/components/watches/WatchStatusPill";
import { Spinner } from "@/components/shared/Spinner";

export function WatchDetailHeader({
  watch,
  onPollNow,
  onToggleEnabled,
  polling = false,
}: {
  watch: Watch;
  onPollNow: () => void;
  // Toggle whether the watch is polling. Parent owns state mutation so
  // the page can reflect the change instantly without a refetch round-
  // trip; the launcher's manager.Patch handles the spawn/teardown of
  // the per-watch poller goroutine.
  onToggleEnabled: (next: boolean) => Promise<void> | void;
  polling?: boolean;
}) {
  const router = useRouter();
  const dialog = useDialog();
  const [togglingEnabled, setTogglingEnabled] = useState(false);
  return (
    <div className="space-y-2">
      <Link
        href="/watches"
        className="inline-flex items-center gap-1.5 text-sm text-zinc-400 transition hover:text-zinc-200"
      >
        <ArrowLeftIcon className="h-3.5 w-3.5" />
        back to watches
      </Link>
      <div className="rounded border border-zinc-800 bg-zinc-900/40">
        <div className="flex items-start justify-between border-b border-zinc-800 px-4 py-3">
          <div className="min-w-0">
            <h1 className="flex items-center gap-2 text-2xl font-semibold text-zinc-100">
              <SourceIcon kind={watch.source.kind} className="h-6 w-6 shrink-0 text-zinc-400" />
              <span className="min-w-0 truncate">{watch.name}</span>
            </h1>
            <div className="mt-0.5 text-sm text-zinc-500">{summary(watch)}</div>
            {watch.description && (
              <div className="mt-2 text-sm text-zinc-300">{watch.description}</div>
            )}
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              disabled={togglingEnabled}
              onClick={async () => {
                setTogglingEnabled(true);
                try {
                  await onToggleEnabled(!watch.enabled);
                } finally {
                  setTogglingEnabled(false);
                }
              }}
              title={watch.enabled ? "Pause polling. Existing config + on-disk items/signals are preserved." : "Resume polling for this watch."}
              className="inline-flex items-center gap-1.5 rounded border border-zinc-700 bg-zinc-900/60 px-3 py-1.5 text-xs text-zinc-300 transition hover:border-zinc-600 hover:text-zinc-100 disabled:cursor-not-allowed disabled:opacity-60"
            >
              {watch.enabled ? "Pause" : "Resume"}
            </button>
            <button
              type="button"
              onClick={onPollNow}
              disabled={polling || !watch.enabled}
              title={!watch.enabled ? "Watch is paused — resume to poll." : undefined}
              className="inline-flex items-center gap-1.5 rounded border border-zinc-700 bg-zinc-900/60 px-3 py-1.5 text-xs text-zinc-300 transition hover:border-zinc-600 hover:text-zinc-100 disabled:cursor-not-allowed disabled:opacity-60 disabled:hover:border-zinc-700 disabled:hover:text-zinc-300"
            >
              {polling ? <Spinner className="h-3.5 w-3.5" /> : "↻"}
              {polling ? "Polling…" : "Poll now"}
            </button>
            <Link
              href={`/watches/${watch.id}/edit`}
              className="inline-flex items-center gap-1.5 rounded border border-zinc-700 bg-zinc-900/60 px-3 py-1.5 text-xs text-zinc-300 transition hover:border-zinc-600 hover:text-zinc-100"
            >
              Edit
            </Link>
            <button
              type="button"
              onClick={async () => {
                const answer = await dialog.prompt({
                  title: `Delete watch "${watch.name}"?`,
                  body: "This removes the watch config and its on-disk items + signals. Type delete to confirm.",
                  placeholder: "delete",
                  confirmLabel: "Delete watch",
                  danger: true,
                  validate: (v) => (v.trim().toLowerCase() === "delete" ? null : `Type "delete" to confirm`),
                });
                if (answer === null) return;
                await watchesAPI.remove(watch.id);
                router.push("/watches");
              }}
              className="inline-flex items-center gap-1.5 rounded border border-red-900/60 bg-red-950/30 px-3 py-1.5 text-xs text-red-300 transition hover:border-red-800 hover:bg-red-950/50 hover:text-red-200"
            >
              Delete
            </button>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-3 px-4 py-2.5">
          <WatchStatusPill status={watch.enabled ? "healthy" : "disabled"} />
          {watch.ingesting && (
            <span
              className="inline-flex items-center gap-1.5 rounded-full bg-amber-900/50 px-2 py-0.5 text-xs text-amber-300"
              title="Ingestion agent is running for this watch."
            >
              <Spinner className="h-3 w-3" />
              ingesting
            </span>
          )}
          {watch.ingest?.enabled && (
            <span
              className="inline-flex items-center gap-1.5 rounded-full bg-violet-900/50 px-2 py-0.5 text-xs text-violet-300"
              title="Ingestion agent runs every poll to classify items into rich signals (investigation_started / unclear / dismissed)."
            >
              auto-ingest
            </span>
          )}
          {watch.autoStart?.enabled && (
            <span
              className="inline-flex items-center gap-1.5 rounded-full bg-pink-900/50 px-2 py-0.5 text-xs text-pink-300"
              title={`Auto-spawns investigations from agent recommendations (max ${watch.autoStart.maxConcurrent ?? 1} concurrent).`}
            >
              auto-start · max {watch.autoStart.maxConcurrent ?? 1}
            </span>
          )}
        </div>
        {watch.ingest?.customInstructions && (
          <div className="border-t border-zinc-800 px-4 pb-3 pt-2">
            <div className="mb-1 text-xs uppercase tracking-wide text-zinc-400">Custom instructions</div>
            <div className="rounded border border-zinc-800 bg-zinc-950 p-3 text-sm text-zinc-300 whitespace-pre-wrap">
              {watch.ingest.customInstructions}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function SourceIcon({ kind, className }: { kind: WatchSourceKind; className?: string }) {
  if (kind === "slack_channel") return <SlackIcon className={className} />;
  return <GitHubIcon className={className} />;
}

function summary(w: Watch) {
  if (w.source.kind === "github_issues") {
    return `github: ${w.source.owner}/${w.source.repo}${(w.source.labels?.length ?? 0) ? ` · labels: ${w.source.labels!.join(", ")}` : ""}`;
  }
  return `slack: #${w.source.channelName ?? w.source.channelID}`;
}
