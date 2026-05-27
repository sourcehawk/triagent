"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { type RepoSummaryStatus } from "@/lib/api";
import { useDialog } from "@/lib/dialog";
import { useStream } from "@/lib/stream";

// Repo-summary state for one repo. Mirrors the server's
// RepoSummaryStatus plus an explicit "inFlight" view that the
// EventSource keeps in sync with phase=started/success/error events.
type RepoState = RepoSummaryStatus;

type Listener = () => void;

type RepoSummaryStore = {
  // get returns undefined when no state has been observed yet for this
  // repo (e.g. the page hasn't fetched /summary/status yet and no event
  // has fired). Consumers treat undefined as "unknown" rather than as a
  // confirmed empty state.
  get(key: string): RepoState | undefined;
  // upsert merges new status into the existing one; partial updates
  // (e.g. phase=started carries no generatedAt) preserve prior values.
  upsert(key: string, partial: Partial<RepoState>): void;
  subscribe(listener: Listener): () => void;
};

const RepoSummaryStoreCtx = createContext<RepoSummaryStore | null>(null);

// repoKey is owner + "/" + name. Stable across the whole app so the
// sidenav row, the repo page, and the EventSource handler all index
// the same map.
export function repoKey(owner: string, name: string): string {
  return `${owner}/${name}`;
}

// useRepoSummaryStatus subscribes the calling component to per-repo
// state. Re-renders the component when state for `owner/name` mutates.
// Returns undefined when no state has been observed yet.
export function useRepoSummaryStatus(
  owner: string,
  name: string,
): RepoState | undefined {
  const store = useContext(RepoSummaryStoreCtx);
  const [, force] = useState(0);
  useEffect(() => {
    if (!store) return;
    return store.subscribe(() => force((n) => n + 1));
  }, [store]);
  if (!store) return undefined;
  return store.get(repoKey(owner, name));
}

// useRepoSummaryStore exposes the imperative side of the store —
// callers can seed initial state from a /summary/status fetch
// (LinkedReposPanel does this on mount). Most consumers want the read
// hook above instead.
export function useRepoSummaryStore(): RepoSummaryStore {
  const store = useContext(RepoSummaryStoreCtx);
  if (!store) {
    throw new Error(
      "useRepoSummaryStore must be used inside <RepoSummaryStateProvider>",
    );
  }
  return store;
}

export function RepoSummaryStateProvider({
  children,
}: {
  children: React.ReactNode;
}) {
  // Map ref so updates don't trigger a top-level re-render — only
  // subscribed components re-render via the listener fan-out. Using
  // the ref directly avoids React's structural-equality checks on a
  // fresh Map per update (cheap on small N but the listener pattern
  // also makes the dependency arrows clearer).
  const mapRef = useRef<Map<string, RepoState>>(new Map());
  const listenersRef = useRef<Set<Listener>>(new Set());
  const dialog = useDialog();
  // Toast id per repo so phase=success or phase=error can update the
  // existing pending toast in place rather than stack.
  const toastsRef = useRef<Map<string, string>>(new Map());

  const notifyListeners = useCallback(() => {
    for (const l of listenersRef.current) l();
  }, []);

  const store = useMemo<RepoSummaryStore>(
    () => ({
      get: (key) => mapRef.current.get(key),
      upsert: (key, partial) => {
        const prev = mapRef.current.get(key);
        const next: RepoState = { ...(prev ?? { exists: false, inFlight: false }), ...partial };
        mapRef.current.set(key, next);
        notifyListeners();
      },
      subscribe: (l) => {
        listenersRef.current.add(l);
        return () => {
          listenersRef.current.delete(l);
        };
      },
    }),
    [notifyListeners],
  );

  const stream = useStream();

  // Subscribe to the multiplex stream's global scope. The single
  // EventSource lives at the layout level (StreamProvider); here we
  // just attach a handler.
  useEffect(() => {
    return stream.subscribe({ scope: "global" }, (env) => {
      if (env.kind !== "repo_summary_state" || !env.repoSummary) return;
      const { owner, name, phase, generatedAt, byteCount, kind, error } = env.repoSummary;
      const key = repoKey(owner, name);

      if (phase === "started") {
        store.upsert(key, { inFlight: true });
        const existing = toastsRef.current.get(key);
        if (existing) {
          dialog.update(existing, {
            kind: "pending",
            title: `Generating architecture summary for ${owner}/${name}…`,
            body: "This can take up to 15 minutes for large repos.",
          });
        } else {
          const id = dialog.notify({
            kind: "pending",
            title: `Generating architecture summary for ${owner}/${name}…`,
            body: "This can take up to 15 minutes for large repos.",
          });
          toastsRef.current.set(key, id);
        }
      } else if (phase === "success") {
        store.upsert(key, {
          exists: true,
          inFlight: false,
          generatedAt,
          byteCount,
          kind,
          error: "",
        });
        const existing = toastsRef.current.get(key);
        const opts = {
          kind: "success" as const,
          title: `Architecture summary ready for ${owner}/${name}`,
          action: {
            label: "view",
            href: `/repos?repo=${encodeURIComponent(`${owner}/${name}`)}`,
          },
        };
        if (existing) dialog.update(existing, opts);
        else toastsRef.current.set(key, dialog.notify(opts));
      } else if (phase === "error") {
        store.upsert(key, { inFlight: false, error: error ?? "unknown error" });
        const existing = toastsRef.current.get(key);
        const opts = {
          kind: "error" as const,
          title: `Architecture summary failed for ${owner}/${name}`,
          body: error ?? "unknown error",
          action: {
            label: "open",
            href: `/repos?repo=${encodeURIComponent(`${owner}/${name}`)}`,
          },
        };
        if (existing) dialog.update(existing, opts);
        else toastsRef.current.set(key, dialog.notify(opts));
      }
    });
  }, [dialog, store, stream]);

  return (
    <RepoSummaryStoreCtx.Provider value={store}>
      {children}
    </RepoSummaryStoreCtx.Provider>
  );
}
