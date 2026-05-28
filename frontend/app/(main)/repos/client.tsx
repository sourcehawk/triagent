"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import {
  api,
  ApiError,
  type LinkedRepo,
  type RepoSummaryStatus,
} from "@/lib/api";
import { GitHubIcon, WarningIcon } from "@/components/Icons";
import { Spinner } from "@/components/Spinner";
import { useDialog } from "@/lib/dialog";
import {
  repoKey,
  useRepoSummaryStatus,
  useRepoSummaryStore,
} from "@/components/RepoSummaryStateProvider";

// ReposIndexClient is the top-level /repos page: a flat list of every
// linked repo (defaults first, then user-added) with its summary
// status and a click-through to /repos?repo=<owner>/<name>. The
// sidenav shows the same data in compressed form; this page is the
// explore-mode complement, useful when the operator wants a wider
// view with descriptions visible inline.
// REPOS_PAGE_SIZE caps each section (defaults / yours) to a single
// page worth of rows so the page doesn't sprawl once an operator has
// linked a long list of repos. The pager renders only when a section
// exceeds this size.
const REPOS_PAGE_SIZE = 5;

export function ReposIndexClient() {
  const router = useRouter();
  const dialog = useDialog();
  const store = useRepoSummaryStore();

  const [defaults, setDefaults] = useState<LinkedRepo[] | null>(null);
  const [user, setUser] = useState<LinkedRepo[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [removing, setRemoving] = useState<string | null>(null);
  // Per-section page indices (0-based). Reset to 0 on reload so a
  // remove that empties the visible page doesn't leave the operator
  // staring at "page 3 of 2".
  const [defaultsPage, setDefaultsPage] = useState(0);
  const [userPage, setUserPage] = useState(0);
  // Bumped whenever a remove succeeds so the load effect re-runs.
  const [reloadNonce, setReloadNonce] = useState(0);
  // Free-text search applied to BOTH sections. Empty string = no
  // filter. Matches owner, name, alias, and description so operators
  // can find a repo by any of the surfaces visible on the row.
  const [filter, setFilter] = useState("");

  useEffect(() => {
    let cancelled = false;
    api
      .listRepos()
      .then((r) => {
        if (cancelled) return;
        setDefaults(r.defaults);
        setUser(r.user);
        setDefaultsPage(0);
        setUserPage(0);
        // Seed the store for every repo so status icons render
        // immediately on a cold load. Per-row hooks pick up live
        // updates from the global SSE stream after that.
        for (const repo of [...r.defaults, ...r.user]) {
          api
            .getRepoSummaryStatus(repo.owner, repo.name)
            .then((s) => {
              if (cancelled) return;
              store.upsert(repoKey(repo.owner, repo.name), s);
            })
            .catch(() => {
              /* non-fatal — row stays without an icon */
            });
        }
      })
      .catch((e) => {
        if (cancelled) return;
        setErr(e instanceof ApiError ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [store, reloadNonce]);

  // Filtered + paginated views of each section. The filter runs over
  // owner, name, alias, and description so a typed query matches
  // anything visible on the row. Page indices are clamped here so
  // shrinking a list (filter, remove) never strands the operator on
  // an empty page.
  const filteredDefaults = useMemo(
    () => (defaults ?? []).filter((r) => matchesRepoFilter(r, filter)),
    [defaults, filter],
  );
  const filteredUser = useMemo(
    () => (user ?? []).filter((r) => matchesRepoFilter(r, filter)),
    [user, filter],
  );
  const defaultsTotalPages = Math.max(
    1,
    Math.ceil(filteredDefaults.length / REPOS_PAGE_SIZE),
  );
  const userTotalPages = Math.max(
    1,
    Math.ceil(filteredUser.length / REPOS_PAGE_SIZE),
  );
  const defaultsPageClamped = Math.min(defaultsPage, defaultsTotalPages - 1);
  const userPageClamped = Math.min(userPage, userTotalPages - 1);
  const defaultsSlice = filteredDefaults.slice(
    defaultsPageClamped * REPOS_PAGE_SIZE,
    (defaultsPageClamped + 1) * REPOS_PAGE_SIZE,
  );
  const userSlice = filteredUser.slice(
    userPageClamped * REPOS_PAGE_SIZE,
    (userPageClamped + 1) * REPOS_PAGE_SIZE,
  );

  // Whenever the filter changes, jump both sections back to page 0
  // so the operator sees results from the top of each list.
  useEffect(() => {
    setDefaultsPage(0);
    setUserPage(0);
  }, [filter]);

  const onRemove = useCallback(
    async (repo: LinkedRepo) => {
      const ok = await dialog.confirm({
        title: `Remove ${repo.owner}/${repo.name}?`,
        body:
          "Future investigations will no longer spawn an MCP server for this repo. Past investigations keep their snapshot. The cached architecture summary on disk is left in place.",
        confirmLabel: "Remove",
        danger: true,
      });
      if (!ok) return;
      const key = repoKey(repo.owner, repo.name);
      setRemoving(key);
      try {
        await api.removeRepo(repo.owner, repo.name);
        setReloadNonce((n) => n + 1);
      } catch (e) {
        const msg = e instanceof ApiError ? e.message : String(e);
        await dialog.alert({
          title: `Could not remove ${repo.owner}/${repo.name}`,
          body: msg,
        });
      } finally {
        setRemoving(null);
      }
    },
    [dialog],
  );

  return (
    <main className="flex-1 overflow-y-auto">
      <div className="mx-auto max-w-6xl px-6 py-10">
        <header className="mb-5">
          <h1 className="text-2xl font-semibold text-zinc-100">
            Linked repositories
          </h1>
          <p className="mt-1 text-xs text-zinc-500">
            Each linked GitHub repo is exposed to the agent as its own
            MCP server during an investigation. Click a row to open
            its architecture summary. Use the{" "}
            <span className="rounded border border-zinc-800 px-1 py-0.5 font-mono text-[10px] text-zinc-400">
              manage
            </span>{" "}
            button in the sidenav to add or remove repos.
          </p>
        </header>

        {err && (
          <div className="mb-4 rounded border border-red-900/60 bg-red-950/40 p-3 text-xs text-red-200/90">
            could not load repos: {err}
          </div>
        )}

        {defaults === null && user === null && !err && (
          <div className="flex items-center gap-2 text-xs text-zinc-500">
            <Spinner className="h-3 w-3" /> loading…
          </div>
        )}

        {(defaults || user) && (
          <div className="mb-5">
            <input
              type="search"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="search repositories…"
              className="w-full rounded border border-zinc-800 bg-zinc-950 px-3 py-1.5 text-sm text-zinc-200 placeholder:text-zinc-600 focus:border-sky-600 focus:outline-none"
            />
          </div>
        )}

        {defaults && defaults.length > 0 && (
          <Section
            group="defaults"
            label={
              filter
                ? `defaults (${filteredDefaults.length}/${defaults.length})`
                : "defaults"
            }
            subtitle="ship with the launcher · read-only"
          >
            {filteredDefaults.length === 0 ? (
              <p className="rounded border border-zinc-800 bg-zinc-900/40 p-3 text-xs text-zinc-500">
                no defaults match &ldquo;{filter}&rdquo;.
              </p>
            ) : (
              <>
                <RepoList
                  repos={defaultsSlice}
                  onClick={(r) =>
                    router.push(
                      `/repos?repo=${encodeURIComponent(`${r.owner}/${r.name}`)}`,
                    )
                  }
                />
                <Pager
                  page={defaultsPageClamped}
                  totalPages={defaultsTotalPages}
                  onPrev={() => setDefaultsPage((p) => Math.max(0, p - 1))}
                  onNext={() =>
                    setDefaultsPage((p) =>
                      Math.min(defaultsTotalPages - 1, p + 1),
                    )
                  }
                />
              </>
            )}
          </Section>
        )}

        {user && (
          <Section
            group="user"
            label={
              filter
                ? `yours (${filteredUser.length}/${user.length})`
                : `yours (${user.length})`
            }
            subtitle="added via the sidenav · persist across launcher restarts"
          >
            {user.length === 0 ? (
              <p className="rounded border border-zinc-800 bg-zinc-900/40 p-3 text-xs text-zinc-500">
                no user-added repos yet. Open the sidenav and click{" "}
                <strong className="text-zinc-300">manage</strong> to add one.
              </p>
            ) : filteredUser.length === 0 ? (
              <p className="rounded border border-zinc-800 bg-zinc-900/40 p-3 text-xs text-zinc-500">
                none of your repos match &ldquo;{filter}&rdquo;.
              </p>
            ) : (
              <>
                <RepoList
                  repos={userSlice}
                  onClick={(r) =>
                    router.push(
                      `/repos?repo=${encodeURIComponent(`${r.owner}/${r.name}`)}`,
                    )
                  }
                  onRemove={onRemove}
                  removingKey={removing}
                />
                <Pager
                  page={userPageClamped}
                  totalPages={userTotalPages}
                  onPrev={() => setUserPage((p) => Math.max(0, p - 1))}
                  onNext={() =>
                    setUserPage((p) => Math.min(userTotalPages - 1, p + 1))
                  }
                />
              </>
            )}
          </Section>
        )}
      </div>
    </main>
  );
}

// matchesRepoFilter is the predicate driving the search bar. Empty
// query matches everything; otherwise we case-insensitive-substring
// across the four surfaces visible on the row (owner, name, alias,
// description) so any of them can drive a hit.
function matchesRepoFilter(repo: LinkedRepo, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  if (repo.owner.toLowerCase().includes(q)) return true;
  if (repo.name.toLowerCase().includes(q)) return true;
  if (repo.alias && repo.alias.toLowerCase().includes(q)) return true;
  if (repo.description && repo.description.toLowerCase().includes(q))
    return true;
  return false;
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
  // Render nothing on a single page — the section is small enough
  // that controls would be visual noise.
  if (totalPages <= 1) return null;
  return (
    <div className="mt-2 flex items-center justify-end gap-2 text-xs text-zinc-500">
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

function Section({
  group,
  label,
  subtitle,
  children,
}: {
  // group is the stable e2e identity for the section ("defaults" |
  // "user"), surfaced as a data attribute so tests locate the linked
  // vs user-local group without depending on the (count-bearing) label.
  group: string;
  label: string;
  subtitle: string;
  children: React.ReactNode;
}) {
  return (
    <section
      className="mb-6"
      data-testid="triagent-repos-group"
      data-repo-group={group}
    >
      <div className="mb-2 flex items-baseline gap-2">
        <h2 className="text-xs font-semibold uppercase tracking-wide text-zinc-400">
          {label}
        </h2>
        <span className="text-xs text-zinc-600">· {subtitle}</span>
      </div>
      {children}
    </section>
  );
}

function RepoList({
  repos,
  onClick,
  onRemove,
  removingKey,
}: {
  repos: LinkedRepo[];
  onClick: (r: LinkedRepo) => void;
  // onRemove is set only for user-added lists. Defaults are read-only
  // (the row's remove control is rendered only when this is provided).
  onRemove?: (r: LinkedRepo) => void;
  // Owner/name key currently being removed — disables that row's
  // remove button while the API call is in flight.
  removingKey?: string | null;
}) {
  return (
    <ul className="space-y-1">
      {repos.map((r) => (
        <RepoRow
          key={`${r.owner}/${r.name}`}
          repo={r}
          onClick={() => onClick(r)}
          onRemove={onRemove ? () => onRemove(r) : undefined}
          removing={removingKey === `${r.owner}/${r.name}`}
        />
      ))}
    </ul>
  );
}

function RepoRow({
  repo,
  onClick,
  onRemove,
  removing,
}: {
  repo: LinkedRepo;
  onClick: () => void;
  onRemove?: () => void;
  removing?: boolean;
}) {
  const status = useRepoSummaryStatus(repo.owner, repo.name);
  return (
    <li
      className="group flex items-stretch gap-2 rounded border border-zinc-800 bg-zinc-900/40 transition hover:border-zinc-600 hover:bg-zinc-900/70"
      data-testid="triagent-repo-row"
      data-repo-key={`${repo.owner}/${repo.name}`}
    >
      <button
        type="button"
        onClick={onClick}
        className="flex flex-1 items-start gap-3 px-3 py-2 text-left"
      >
        <GitHubIcon className="mt-0.5 h-4 w-4 shrink-0 text-zinc-500 group-hover:text-zinc-300" />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="truncate font-mono text-sm text-zinc-100">
              {repo.owner}/{repo.name}
            </span>
            {repo.alias && (
              <span className="truncate font-mono text-[10px] text-zinc-500">
                · alias {repo.alias}
              </span>
            )}
          </div>
          {repo.description && (
            <div className="mt-0.5 line-clamp-2 text-xs text-zinc-500">
              {repo.description}
            </div>
          )}
        </div>
        <div className="shrink-0 self-center">
          <RepoStatusBadge status={status} />
        </div>
      </button>
      {onRemove && (
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            onRemove();
          }}
          disabled={removing}
          aria-label={`remove ${repo.owner}/${repo.name}`}
          className="shrink-0 self-stretch border-l border-zinc-800 px-3 text-xs text-zinc-500 transition hover:bg-red-950/40 hover:text-red-300 disabled:cursor-not-allowed disabled:opacity-40"
        >
          {removing ? "removing…" : "remove"}
        </button>
      )}
    </li>
  );
}

function RepoStatusBadge({
  status,
}: {
  status: RepoSummaryStatus | undefined;
}) {
  if (!status) {
    return <span className="text-[10px] text-zinc-600">…</span>;
  }
  if (status.inFlight) {
    return (
      <span className="flex items-center gap-1 text-[10px] text-sky-300">
        <span
          className="inline-block h-2.5 w-2.5 animate-spin rounded-full border-2 border-current border-r-transparent"
          aria-hidden
        />
        generating
      </span>
    );
  }
  if (status.error) {
    return (
      <span
        title={status.error}
        className="text-[10px] text-red-400"
      >
        ⨯ failed
      </span>
    );
  }
  if (status.exists) {
    const when = status.generatedAt
      ? new Date(status.generatedAt).toLocaleDateString()
      : "";
    return (
      <span className="text-[10px] text-emerald-400">
        ✓ {when || "cached"}
      </span>
    );
  }
  // No cached summary yet — surface as a warning so the operator
  // notices and can click through to generate one.
  return (
    <span
      title="open the repo page to generate one"
      className="flex items-center gap-1 text-[10px] text-amber-400"
    >
      <WarningIcon className="h-3 w-3 shrink-0" aria-hidden />
      no summary yet
    </span>
  );
}
