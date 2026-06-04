"use client";

import { useEffect, useState } from "react";
import { api, ApiError, type LinkedRepo } from "@/lib/api";
import { notifyReposChanged, onReposChanged } from "@/lib/repos-events";
import { useDialog } from "@/lib/dialog";
import { Spinner } from "@/components/shared/Spinner";
import { MIN_DESCRIPTION_LENGTH } from "./LinkedReposPanel";

export function ManageReposModal({
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

  // changeNonce reloads the modal both on its own add/remove and on
  // changes made elsewhere (e.g. the /repos page) while it's open.
  const [changeNonce, setChangeNonce] = useState(0);
  useEffect(() => onReposChanged(() => setChangeNonce((n) => n + 1)), []);

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
  }, [refreshNonce, changeNonce]);

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
      notifyReposChanged();
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
      notifyReposChanged();
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

export function RepoRow({
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

export function Section({
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
