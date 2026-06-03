"use client";

import { useCallback, useEffect, useState } from "react";
import { wikiApi, ApiError } from "@/lib/wiki-api";
import type { WikiUpstreamStatus } from "@/lib/wiki-api";
import {
  ExternalLinkIcon,
  GitHubIcon,
  SyncIcon,
} from "@/components/shared/Icons";

type SyncState =
  | { kind: "idle" }
  | { kind: "syncing" }
  | { kind: "error"; message: string };

type Props = {
  // Fires once a sync (fetch + reset --hard) has completed successfully.
  // Consumers use it to refresh views whose data is now stale — most
  // visibly the entry list, where post-sync rows may flip from
  // unsynced → synced and need to drop the cloud-up icon.
  onSynced?: () => void;
};

export function WikiUpstreamHeader({ onSynced }: Props = {}) {
  const [upstream, setUpstream] = useState<WikiUpstreamStatus | null>(null);
  const [syncState, setSyncState] = useState<SyncState>({ kind: "idle" });
  // Distinguish "haven't tried yet" from "tried and got nothing" so we
  // can show a skeleton during the in-flight fetch (which usually takes
  // ~1s — long enough that an empty space is jarring).
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    let cancelled = false;
    wikiApi
      .getUpstreamStatus()
      .then((s) => {
        if (cancelled) return;
        setUpstream(s);
        setLoaded(true);
      })
      .catch(() => {
        if (cancelled) return;
        setUpstream(null);
        setLoaded(true);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const sync = useCallback(async () => {
    setSyncState({ kind: "syncing" });
    try {
      await wikiApi.syncUpstream();
      // Re-fetch status so the header stamp reflects the new HEAD.
      const status = await wikiApi.getUpstreamStatus();
      setUpstream(status);
      setSyncState({ kind: "idle" });
      onSynced?.();
    } catch (e) {
      setSyncState({
        kind: "error",
        message: e instanceof ApiError ? e.message : String(e),
      });
    }
  }, [onSynced]);

  if (!loaded) {
    return <UpstreamHeaderSkeleton />;
  }

  if (!upstream) {
    return null;
  }

  // If error is "no wiki path is configured" don't show the header at all —
  // WikiHome will already show the not-configured screen.
  if (upstream.error && !upstream.gitCheckout) {
    return null;
  }

  const canSync =
    upstream.gitCheckout && !!upstream.repo && syncState.kind !== "syncing";
  const repoUrl = upstream.repo
    ? `https://github.com/${upstream.repo}`
    : null;
  const remoteAhead = upstream.remoteAhead ?? 0;

  return (
    <div className="flex flex-col items-end gap-1">
      <div className="flex items-center gap-6 text-xs text-zinc-500">
        <span title={upstream.lastSynced || ""}>
          last synced:{" "}
          <span className="text-zinc-300">
            {upstream.lastSynced ? relativeTime(upstream.lastSynced) : "never"}
          </span>
        </span>
        {repoUrl ? (
          <a
            href={repoUrl}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1.5 text-zinc-400 transition hover:text-zinc-100"
          >
            <GitHubIcon className="h-3.5 w-3.5" />
            <span>
              synced from:{" "}
              <span className="font-mono text-zinc-300">{upstream.repo}</span>
            </span>
            <ExternalLinkIcon className="h-3 w-3 opacity-60" />
          </a>
        ) : (
          <span>
            synced from: <span className="font-mono">(unset)</span>
          </span>
        )}
        {upstream.commit && (
          <span className="font-mono text-zinc-500">
            {upstream.commit}
          </span>
        )}
        <button
          type="button"
          onClick={() => void sync()}
          disabled={!canSync}
          title={
            !upstream.gitCheckout
              ? "wiki dir is not a git checkout — sync needs a cloned repo"
              : !upstream.repo
                ? "no upstream repo configured"
                : remoteAhead > 0
                  ? `${remoteAhead} upstream commit${remoteAhead === 1 ? "" : "s"} not yet applied`
                  : "git fetch + reset --hard origin/HEAD"
          }
          className={
            "inline-flex items-center gap-1.5 rounded border px-2.5 py-1 text-xs transition " +
            (canSync
              ? "border-zinc-700 text-zinc-200 hover:border-zinc-500 hover:text-zinc-100"
              : "border-zinc-800 text-zinc-600")
          }
        >
          <SyncIcon
            className={
              "h-3.5 w-3.5 " +
              (syncState.kind === "syncing" ? "animate-spin" : "")
            }
          />
          <span>{syncState.kind === "syncing" ? "syncing…" : "sync"}</span>
          {remoteAhead > 0 && syncState.kind !== "syncing" && (
            <span className="ml-1 rounded-full bg-amber-500/80 px-1.5 py-0.5 text-xs font-semibold text-zinc-950">
              {remoteAhead}
            </span>
          )}
        </button>
      </div>
      {remoteAhead > 0 && syncState.kind !== "syncing" && (
        <div className="text-xs text-amber-400/80">
          upstream has {remoteAhead} commit{remoteAhead === 1 ? "" : "s"} not
          yet applied locally — click sync to pull.
        </div>
      )}
      {upstream.remoteAheadError && remoteAhead === 0 && (
        <div className="max-w-[40rem] truncate text-xs text-zinc-600">
          (couldn't check upstream: {upstream.remoteAheadError})
        </div>
      )}
      {syncState.kind === "error" && (
        <div className="max-w-[40rem] truncate text-xs text-red-400">
          {syncState.message}
        </div>
      )}
    </div>
  );
}

// UpstreamHeaderSkeleton reserves the space the loaded header occupies
// while the upstream-status fetch is in flight. Three pulsing
// placeholders matching the loaded layout's three slots (last synced,
// synced from, sync button) keep the surrounding chrome from jumping
// when the real header lands ~1s after page load.
function UpstreamHeaderSkeleton() {
  return (
    <div
      className="flex flex-col items-end gap-1"
      aria-busy
      aria-label="loading github sync status"
    >
      <div className="flex items-center gap-6">
        <div className="h-4 w-28 animate-pulse rounded bg-zinc-800/60" />
        <div className="h-4 w-48 animate-pulse rounded bg-zinc-800/60" />
        <div className="h-6 w-16 animate-pulse rounded bg-zinc-800/60" />
      </div>
    </div>
  );
}

// relativeTime renders an ISO-8601 timestamp as "5m ago" / "2h ago" / "3d ago".
function relativeTime(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const seconds = Math.floor((Date.now() - t) / 1000);
  if (seconds < 60) return `${seconds}s ago`;
  const m = Math.floor(seconds / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h}h ago`;
  const d = Math.floor(h / 24);
  return `${d}d ago`;
}
