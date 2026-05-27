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
import { useStream } from "@/lib/stream";

// CodefixPRState mirrors the launcher's codefix_pr_state envelope
// payload. State is the PR lifecycle phase; timestamps are RFC3339 or
// undefined when not applicable.
export type CodefixPRState = {
  state: string; // "open" | "merged" | "closed" | "draft" | ""
  mergedAt?: string;
  closedAt?: string;
};

type Listener = () => void;

type CodefixPRStateStore = {
  // get returns undefined when no state has been observed yet for
  // this PR URL (e.g. the poller hasn't ticked yet). Consumers fall
  // back to their locally-fetched value.
  get(prURL: string): CodefixPRState | undefined;
  upsert(prURL: string, partial: Partial<CodefixPRState>): void;
  subscribe(listener: Listener): () => void;
};

export const CodefixPRStateStoreCtx = createContext<CodefixPRStateStore | null>(null);

// useCodefixPRState subscribes the calling component to per-URL state.
// Re-renders the component when state for the URL mutates. Returns
// undefined when no state has been observed yet — callers prefer
// their own fetched state in that case.
export function useCodefixPRState(prURL: string): CodefixPRState | undefined {
  const store = useContext(CodefixPRStateStoreCtx);
  const [, force] = useState(0);
  useEffect(() => {
    if (!store) return;
    return store.subscribe(() => force((n) => n + 1));
  }, [store]);
  if (!store) return undefined;
  return store.get(prURL);
}

export function CodefixPRStateProvider({
  children,
}: {
  children: React.ReactNode;
}) {
  const mapRef = useRef<Map<string, CodefixPRState>>(new Map());
  const listenersRef = useRef<Set<Listener>>(new Set());

  const notifyListeners = useCallback(() => {
    for (const l of listenersRef.current) l();
  }, []);

  const store = useMemo<CodefixPRStateStore>(
    () => ({
      get: (prURL) => mapRef.current.get(prURL),
      upsert: (prURL, partial) => {
        const prev = mapRef.current.get(prURL);
        const next: CodefixPRState = { ...(prev ?? { state: "" }), ...partial };
        mapRef.current.set(prURL, next);
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
  // just attach a handler. Mirrors RepoSummaryStateProvider.
  useEffect(() => {
    return stream.subscribe({ scope: "global" }, (env) => {
      if (env.kind !== "codefix_pr_state" || !env.codefixPRState) return;
      const p = env.codefixPRState;
      store.upsert(p.url, {
        state: p.state,
        mergedAt: p.mergedAt,
        closedAt: p.closedAt,
      });
    });
  }, [store, stream]);

  return (
    <CodefixPRStateStoreCtx.Provider value={store}>
      {children}
    </CodefixPRStateStoreCtx.Provider>
  );
}
