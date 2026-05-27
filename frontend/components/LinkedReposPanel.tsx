"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiError, type Investigation, type LinkedRepo, type RepoSummaryStatus } from "@/lib/api";
import { useDialog } from "@/lib/dialog";
import { Spinner } from "./Spinner";
import {
  useRepoSummaryStatus,
  useRepoSummaryStore,
  repoKey,
} from "@/components/RepoSummaryStateProvider";
import { GitHubIcon, WarningIcon } from "@/components/Icons";

type Props = {
  // Active investigation, when one is open. Drives the read-only "linked
  // for this session" list. Null when no investigation is active — the
  // panel still renders with the manage button so users can curate the
  // list before starting a new investigation.
  investigation: Investigation | null;
  // Bumped by the parent on session changes so we re-fetch the global
  // repo lists if the user navigates away and back.
  refreshNonce?: number;
};

const EXPANDED_KEY = "triagent.linkedrepos.expanded";

// MIN_DESCRIPTION_LENGTH mirrors repos.MinDescriptionLength on the Go side.
// Hard-coded constant rather than fetched from the server to avoid a
// network round-trip on form mount; if the backend ever raises the
// minimum, update here too. The backend rejection is the source of truth.
const MIN_DESCRIPTION_LENGTH = 30;

function readExpanded(): boolean {
  if (typeof window === "undefined") return false;
  return window.localStorage.getItem(EXPANDED_KEY) === "1";
}

export function LinkedReposPanel({ investigation, refreshNonce }: Props) {
  const [manageOpen, setManageOpen] = useState(false);
  // Collapsed by default so the panel only takes the header strip
  // (~40px) at the bottom of the sidebar; expanding it overlays
  // upward into the history list. Persisted so an operator who keeps
  // it pinned open doesn't have to re-toggle on every reload.
  const [expanded, setExpanded] = useState<boolean>(readExpanded);

  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem(EXPANDED_KEY, expanded ? "1" : "0");
  }, [expanded]);

  // Listen for the launcher-wide "open the manage-repos modal" event.
  // The /repos page's primary "+ add repository" sidebar action
  // (dispatched from (main)/layout.tsx onNew) fires this so we don't
  // have to lift the modal's state up to the layout.
  useEffect(() => {
    const handler = () => setManageOpen(true);
    window.addEventListener("triagent:open-manage-repos", handler);
    return () =>
      window.removeEventListener("triagent:open-manage-repos", handler);
  }, []);

  return (
    <div className="border-t border-zinc-800">
      {/* Header strip is the only chrome the panel takes when collapsed.
          The whole row toggles expansion; the inner "manage" target
          stops propagation so it opens the modal without flipping the
          collapse state. */}
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        aria-expanded={expanded}
        className="flex w-full items-center justify-between gap-2 px-3 py-2.5 text-left transition hover:bg-zinc-900/40"
      >
        <span className="flex items-baseline gap-1.5 text-xs uppercase tracking-wide text-zinc-500">
          <span aria-hidden className="inline-block w-2 text-zinc-600">
            {expanded ? "▾" : "▴"}
          </span>
          linked github projects
        </span>
        <span
          role="button"
          tabIndex={0}
          onClick={(e) => {
            e.stopPropagation();
            setManageOpen(true);
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter" || e.key === " ") {
              e.preventDefault();
              e.stopPropagation();
              setManageOpen(true);
            }
          }}
          className="rounded border border-zinc-800 px-1.5 py-0.5 text-xs text-zinc-400 transition hover:border-zinc-600 hover:text-zinc-200"
        >
          manage
        </span>
      </button>

      {expanded && (
        <div className="max-h-64 overflow-y-auto px-3 pb-3">
          {investigation ? (
            <>
              <p className="mb-1 px-1 text-xs text-zinc-600">
                linked to this investigation:
              </p>
              <ActiveReposList linkedRepos={investigation.linkedRepos ?? []} />
            </>
          ) : (
            <PendingReposList refreshNonce={refreshNonce} />
          )}
        </div>
      )}

      {manageOpen && (
        <ManageReposModal
          onClose={() => setManageOpen(false)}
          refreshNonce={refreshNonce}
        />
      )}
    </div>
  );
}

// PendingReposList is the no-active-investigation sidebar variant: it
// shows the resolved defaults + user_repos that *will* be linked once an
// investigation starts, so the operator can confirm their list before
// kicking one off. Same shape as ActiveReposList; differs only in
// its hint copy and that it pulls from /api/repos rather than the
// frozen Investigation.linkedRepos snapshot.
function PendingReposList({ refreshNonce }: { refreshNonce?: number }) {
  const [repos, setRepos] = useState<LinkedRepo[] | null>(null);
  const [err, setErr] = useState<string | null>(null);

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
  }, [refreshNonce]);

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

function ActiveReposList({ linkedRepos }: { linkedRepos: LinkedRepo[] }) {
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

function RepoRowMinimal({
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

function RepoStatusGlyph({ status }: { status: RepoSummaryStatus | undefined }) {
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

function ManageReposModal({
  onClose,
  refreshNonce,
}: {
  onClose: () => void;
  refreshNonce?: number;
}) {
  const [defaults, setDefaults] = useState<LinkedRepo[] | null>(null);
  const [user, setUser] = useState<LinkedRepo[] | null>(null);
  const [loadErr, setLoadErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [actionErr, setActionErr] = useState<string | null>(null);
  const dialog = useDialog();

  // Add-form state.
  const [owner, setOwner] = useState("");
  const [name, setName] = useState("");
  const [alias, setAlias] = useState("");
  const [description, setDescription] = useState("");

  function reload() {
    setLoadErr(null);
    api
      .listRepos()
      .then((r) => {
        setDefaults(r.defaults);
        setUser(r.user);
      })
      .catch((e) => setLoadErr(e instanceof ApiError ? e.message : String(e)));
  }

  useEffect(() => {
    reload();
  }, [refreshNonce]);

  async function add(e: React.FormEvent) {
    e.preventDefault();
    if (!owner.trim() || !name.trim()) return;
    if (description.trim().length < MIN_DESCRIPTION_LENGTH) return;
    setBusy(true);
    setActionErr(null);
    try {
      await api.addRepo({
        owner: owner.trim(),
        name: name.trim(),
        alias: alias.trim() || undefined,
        description: description.trim(),
      });
      setOwner("");
      setName("");
      setAlias("");
      setDescription("");
      reload();
    } catch (err) {
      setActionErr(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function remove(r: LinkedRepo) {
    const ok = await dialog.confirm({
      title: `Remove ${r.owner}/${r.name}?`,
      body: "Future investigations will no longer spawn an MCP server for this repo. Past investigations keep their snapshot.",
      confirmLabel: "Remove",
      danger: true,
    });
    if (!ok) return;
    setBusy(true);
    setActionErr(null);
    try {
      await api.removeRepo(r.owner, r.name);
      reload();
    } catch (err) {
      setActionErr(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4">
      <div className="max-h-[80vh] w-full max-w-lg overflow-y-auto rounded border border-zinc-800 bg-zinc-950 p-5 text-sm shadow-xl">
        <div className="mb-4 flex items-center justify-between">
          <h3 className="text-base font-medium text-zinc-100">
            Linked GitHub repositories
          </h3>
          <button
            type="button"
            onClick={onClose}
            className="text-zinc-500 transition hover:text-zinc-200"
            aria-label="close"
          >
            ✕
          </button>
        </div>

        <p className="mb-4 text-xs text-zinc-500">
          Each linked repo gets its own MCP server in new investigations.
          Defaults are read-only; entries you add here persist across launcher
          restarts.
        </p>

        {loadErr && (
          <div className="mb-3 rounded border border-red-900/60 bg-red-950/40 px-2 py-1.5 text-xs text-red-200/80">
            {loadErr}
          </div>
        )}

        {defaults === null && !loadErr && (
          <div className="flex items-center gap-2 text-xs text-zinc-500">
            <Spinner className="h-3 w-3" /> loading…
          </div>
        )}

        {defaults && defaults.length > 0 && (
          <Section label="defaults">
            <ul className="space-y-1">
              {defaults.map((r) => (
                <RepoRow key={`d-${r.owner}/${r.name}`} repo={r} locked />
              ))}
            </ul>
          </Section>
        )}

        {user && (
          <Section label={`yours (${user.length})`}>
            {user.length === 0 ? (
              <p className="text-xs text-zinc-600">
                you haven't added any repos yet.
              </p>
            ) : (
              <ul className="space-y-1">
                {user.map((r) => (
                  <RepoRow
                    key={`u-${r.owner}/${r.name}`}
                    repo={r}
                    onRemove={() => remove(r)}
                    busy={busy}
                  />
                ))}
              </ul>
            )}
          </Section>
        )}

        <Section label="add a repo">
          <form onSubmit={add} className="space-y-2">
            <div className="grid grid-cols-2 gap-2">
              <input
                type="text"
                placeholder="owner"
                value={owner}
                onChange={(e) => setOwner(e.target.value)}
                className="rounded border border-zinc-800 bg-zinc-900 px-2 py-1 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
                required
              />
              <input
                type="text"
                placeholder="name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                className="rounded border border-zinc-800 bg-zinc-900 px-2 py-1 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
                required
              />
            </div>
            <input
              type="text"
              placeholder="alias (optional, defaults to name)"
              value={alias}
              onChange={(e) => setAlias(e.target.value)}
              className="w-full rounded border border-zinc-800 bg-zinc-900 px-2 py-1 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
            />
            <textarea
              placeholder={`description (required, min ${MIN_DESCRIPTION_LENGTH} chars) — what's in the repo and when to consult it. Acts as the agent's first-stop orientation when no architecture summary has been generated yet.`}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              required
              minLength={MIN_DESCRIPTION_LENGTH}
              rows={3}
              className="w-full rounded border border-zinc-800 bg-zinc-900 px-2 py-1 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
            />
            <div className="flex items-center justify-between">
              {actionErr ? (
                <span className="text-xs text-red-400">{actionErr}</span>
              ) : (
                <span className="text-xs text-zinc-600">
                  {description.trim().length > 0 && description.trim().length < MIN_DESCRIPTION_LENGTH
                    ? `description: ${description.trim().length}/${MIN_DESCRIPTION_LENGTH} chars`
                    : "applies to new investigations"}
                </span>
              )}
              <button
                type="submit"
                disabled={
                  busy ||
                  !owner.trim() ||
                  !name.trim() ||
                  description.trim().length < MIN_DESCRIPTION_LENGTH
                }
                className="rounded bg-zinc-100 px-3 py-1 text-xs font-medium text-zinc-900 transition hover:bg-white disabled:cursor-not-allowed disabled:bg-zinc-700 disabled:text-zinc-400"
              >
                {busy ? "saving…" : "add"}
              </button>
            </div>
          </form>
        </Section>
      </div>
    </div>
  );
}

function RepoRow({
  repo,
  locked,
  onRemove,
  busy,
}: {
  repo: LinkedRepo;
  locked?: boolean;
  onRemove?: () => void;
  busy?: boolean;
}) {
  return (
    <li className="flex items-start justify-between gap-2 rounded border border-zinc-800 bg-zinc-900/40 px-2 py-1.5">
      <div className="min-w-0">
        <div className="font-mono text-xs text-zinc-100">
          {repo.owner}/{repo.name}
        </div>
        {(repo.alias || repo.description) && (
          <div className="text-xs text-zinc-500">
            {repo.alias && <span className="font-mono">{repo.alias}</span>}
            {repo.alias && repo.description && " · "}
            {repo.description}
          </div>
        )}
      </div>
      {locked ? (
        <span title="default — not deletable" className="text-xs text-zinc-600">
          🔒
        </span>
      ) : (
        <button
          type="button"
          onClick={onRemove}
          disabled={busy}
          className="text-xs text-zinc-500 transition hover:text-red-400 disabled:opacity-40"
          aria-label={`remove ${repo.owner}/${repo.name}`}
        >
          remove
        </button>
      )}
    </li>
  );
}

function Section({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="mb-4">
      <div className="mb-1 text-xs uppercase tracking-wide text-zinc-500">
        {label}
      </div>
      {children}
    </div>
  );
}
