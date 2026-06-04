import {
  ExternalLinkIcon,
  GitHubIcon,
  SyncIcon,
} from "@/components/shared/Icons";
import { type PlaybooksUpstreamStatus } from "@/lib/api";
import { type UpstreamRepoSyncState } from "./PlaybookList";

// Pagination is a compact prev/next + "page X of Y" control rendered
// below the playbook grid. Hidden when the filtered count fits on a
// single page; otherwise sits flush against the cards above.
export function Pagination({
  page,
  pageSize,
  total,
  totalPages,
  onChange,
}: {
  page: number;
  pageSize: number;
  total: number;
  totalPages: number;
  onChange: (next: number) => void;
}) {
  const start = page * pageSize + 1;
  const end = Math.min((page + 1) * pageSize, total);
  const canPrev = page > 0;
  const canNext = page < totalPages - 1;
  return (
    <div className="mt-3 flex items-center justify-between border-t border-zinc-800 pt-3 text-xs text-zinc-500">
      <span>
        showing <span className="text-zinc-300">{start}–{end}</span> of{" "}
        <span className="text-zinc-300">{total}</span>
      </span>
      <div className="flex items-center gap-1">
        <button
          type="button"
          onClick={() => onChange(page - 1)}
          disabled={!canPrev}
          aria-label="previous page"
          className="rounded border border-zinc-800 px-2 py-1 text-xs text-zinc-300 transition hover:border-zinc-600 hover:text-zinc-100 disabled:cursor-not-allowed disabled:border-zinc-900 disabled:text-zinc-700"
        >
          ‹ prev
        </button>
        <span className="px-2 font-mono text-xs text-zinc-400">
          {page + 1} / {totalPages}
        </span>
        <button
          type="button"
          onClick={() => onChange(page + 1)}
          disabled={!canNext}
          aria-label="next page"
          className="rounded border border-zinc-800 px-2 py-1 text-xs text-zinc-300 transition hover:border-zinc-600 hover:text-zinc-100 disabled:cursor-not-allowed disabled:border-zinc-900 disabled:text-zinc-700"
        >
          next ›
        </button>
      </div>
    </div>
  );
}

// UpstreamFooter is the three-div strip anchored to the right of the
// page header, mirroring the wiki layout. Layout (left → right):
//
//   1. last synced: <relative time>
//   2. synced from: <repo>  (clickable, opens GitHub)
//   3. [sync icon] sync   (badge with N when remote has commits ahead)
//
// Padded apart with a gap class so each pieces stays its own
// readable block instead of running together. Errors stack below.
export function UpstreamFooter({
  upstream,
  loaded,
  syncState,
  onSync,
}: {
  upstream: PlaybooksUpstreamStatus | null;
  loaded: boolean;
  syncState: UpstreamRepoSyncState;
  onSync: () => void | Promise<void>;
}) {
  if (!loaded) {
    return <UpstreamFooterSkeleton />;
  }
  if (!upstream) {
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
        <button
          type="button"
          onClick={() => void onSync()}
          disabled={!canSync}
          title={
            !upstream.gitCheckout
              ? "playbooks dir is not a git checkout — sync needs a cloned repo"
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

// UpstreamFooterSkeleton reserves the space the loaded footer
// occupies while the upstream-status fetch is in flight. Three
// pulsing placeholders matching the loaded layout's three slots
// (last synced, synced from, sync button) keep the surrounding
// chrome from jumping when the real footer lands ~1s after page
// load.
function UpstreamFooterSkeleton() {
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

// relativeTime renders an ISO-8601 timestamp as "5m ago" / "2h ago"
// / "3d ago". Falls back to the raw string for parse failures so the
// operator still sees something useful.
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
