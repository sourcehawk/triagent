"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiError, type LinkedRepo, type RepoSummaryStatus } from "@/lib/api";
import { onReposChanged } from "@/lib/repos-events";
import { Spinner } from "@/components/shared/Spinner";
import {
  useRepoSummaryStatus,
  useRepoSummaryStore,
  repoKey,
} from "@/components/repos/RepoSummaryStateProvider";
import { GitHubIcon, WarningIcon } from "@/components/shared/Icons";

// PendingReposList is the no-active-investigation sidebar variant: it
// shows the resolved defaults + user_repos that *will* be linked once an
// investigation starts, so the operator can confirm their list before
// kicking one off. Same shape as ActiveReposList; differs only in
// its hint copy and that it pulls from /api/repos rather than the
// frozen Investigation.linkedRepos snapshot.
export function PendingReposList({ refreshNonce }: { refreshNonce?: number }) {
  const [repos, setRepos] = useState<LinkedRepo[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  // Refetch when a repo is added/removed from any surface (the manage
  // modal here, or the /repos page) so the sidebar list stays in sync.
  const [changeNonce, setChangeNonce] = useState(0);
  useEffect(() => onReposChanged(() => setChangeNonce((n) => n + 1)), []);

  useEffect(() => {
    let cancelled = false;
    api
      .listRepos()
      .then((r) => {
        if (cancelled) return;
        // Defaults first, then user repos — matches launcher resolution
        // order so the sidebar mirrors what the next investigation gets.
        setRepos([...r.defaults, ...r.user]);
        setErr(null);
      })
      .catch((e) => {
        if (cancelled) return;
        setErr(e instanceof ApiError ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [refreshNonce, changeNonce]);

  if (err) {
    return (
      <p className="px-1 text-xs text-red-300/80">
        couldn't load repos: {err}
      </p>
    );
  }
  if (repos === null) {
    return (
      <div className="flex items-center gap-2 px-1 text-xs text-zinc-600">
        <Spinner className="h-3 w-3" /> loading…
      </div>
    );
  }
  if (repos.length === 0) {
    return (
      <p className="px-1 text-xs text-zinc-600">
        no repos configured. add some via "manage".
      </p>
    );
  }
  return (
    <>
      <p className="mb-1 px-1 text-xs text-zinc-600">
        will be linked when you start an investigation:
      </p>
      <ActiveReposList linkedRepos={repos} />
    </>
  );
}

export function ActiveReposList({ linkedRepos }: { linkedRepos: LinkedRepo[] }) {
  const router = useRouter();
  const store = useRepoSummaryStore();

  // Seed the store for each repo on mount so the row renders the
  // correct status icon before any SSE event fires. We don't await
  // the responses — the rows render with no icon and update in place
  // as fetches complete. Keyed effect so re-renders after a list
  // change still trigger fresh fetches (cheap; the endpoint reads
  // frontmatter only).
  useEffect(() => {
    let cancelled = false;
    for (const r of linkedRepos) {
      api
        .getRepoSummaryStatus(r.owner, r.name)
        .then((s) => {
          if (cancelled) return;
          store.upsert(repoKey(r.owner, r.name), s);
        })
        .catch(() => {
          /* non-fatal — row stays without an icon */
        });
    }
    return () => {
      cancelled = true;
    };
  }, [linkedRepos, store]);

  if (linkedRepos.length === 0) {
    return (
      <p className="px-1 text-xs text-zinc-600">
        none linked to this session.
      </p>
    );
  }
  return (
    <ul className="space-y-0.5">
      {linkedRepos.map((r) => (
        <RepoRowMinimal
          key={`${r.owner}/${r.name}`}
          repo={r}
          onClick={() =>
            router.push(
              `/repos?repo=${encodeURIComponent(`${r.owner}/${r.name}`)}`,
            )
          }
        />
      ))}
    </ul>
  );
}

export function RepoRowMinimal({
  repo,
  onClick,
}: {
  repo: LinkedRepo;
  onClick: () => void;
}) {
  const status = useRepoSummaryStatus(repo.owner, repo.name);
  // Tooltip: owner/name on the first line, the connection-time
  // description on the second. Keeps the row line-itself terse while
  // preserving the description the operator authored at add time.
  const tooltip =
    repo.description && repo.description.length > 0
      ? `${repo.owner}/${repo.name} — ${repo.description}`
      : `${repo.owner}/${repo.name}`;
  return (
    <li>
      <button
        type="button"
        onClick={onClick}
        title={tooltip}
        className="flex w-full items-center gap-2 rounded px-1.5 py-1 text-left text-xs text-zinc-200 transition hover:bg-zinc-900/60"
      >
        <GitHubIcon className="h-3.5 w-3.5 shrink-0 text-zinc-500" />
        <span className="flex-1 truncate font-mono">
          {repo.owner}/{repo.name}
        </span>
        <RepoStatusGlyph status={status} />
      </button>
    </li>
  );
}

export function RepoStatusGlyph({ status }: { status: RepoSummaryStatus | undefined }) {
  if (!status) return <span className="w-3" aria-hidden />; // placeholder for layout stability
  if (status.inFlight) {
    return (
      <span
        title="generating architecture summary…"
        className="inline-block h-3 w-3 animate-spin rounded-full border-2 border-current border-r-transparent text-sky-400"
        aria-hidden
      />
    );
  }
  if (status.error) {
    return (
      <span
        title={`generation failed: ${status.error}`}
        className="text-xs text-red-400"
        aria-hidden
      >
        ⨯
      </span>
    );
  }
  if (status.exists) {
    return (
      <span
        title="architecture summary cached"
        className="text-xs text-emerald-500"
        aria-hidden
      >
        ✓
      </span>
    );
  }
  // No cached summary yet. Surface this explicitly — without an icon
  // here the row looks identical to a healthy one and operators won't
  // know to trigger generation. Click-through to the repo page lets
  // them refresh.
  return (
    <span
      title="no architecture summary yet — click to generate"
      className="flex items-center text-amber-400"
    >
      <WarningIcon className="h-3.5 w-3.5 shrink-0" aria-hidden />
    </span>
  );
}
