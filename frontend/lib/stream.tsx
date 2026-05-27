"use client";

import {
  createContext,
  useContext,
  useEffect,
  useRef,
  type ReactNode,
} from "react";
import { api, type StreamEnvelope } from "./api";
import { matchesFilter, type StreamFilter } from "./stream-dispatch";

export type { StreamFilter } from "./stream-dispatch";

type Handler = (env: StreamEnvelope) => void;

type Subscription = {
  filter: StreamFilter;
  handler: Handler;
};

type StreamContext = {
  subscribe: (filter: StreamFilter, handler: Handler) => () => void;
};

const Ctx = createContext<StreamContext | null>(null);

// StreamProvider owns the single EventSource at /api/stream for the
// page lifetime. Consumers subscribe via useStream() with a filter;
// their handler receives every envelope matching that filter.
//
// Connection lifecycle:
// - mount: open EventSource with a per-tab UUID
// - SPA navigation: nothing — the connection persists
// - tab close: browser unloads, kernel sends FIN, server's heartbeat
//   reaps within 2s
//
// Reconnect: the browser auto-retries with Last-Event-ID; the server
// replays from its ring buffer.
export function StreamProvider({ children }: { children: ReactNode }) {
  const subsRef = useRef<Set<Subscription>>(new Set());
  const ctxRef = useRef<StreamContext | null>(null);

  if (ctxRef.current === null) {
    ctxRef.current = {
      subscribe: (filter, handler) => {
        const sub: Subscription = { filter, handler };
        subsRef.current.add(sub);
        return () => {
          subsRef.current.delete(sub);
        };
      },
    };
  }

  useEffect(() => {
    const connToken =
      typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
        ? crypto.randomUUID()
        : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
    const es = new EventSource(api.streamURL(connToken), { withCredentials: true });

    // Single message handler — Chrome's EventSource.addEventListener
    // routes by `event:` line, which we use server-side as Kind. We
    // attach to each kind we know the server emits.
    const dispatch = (e: MessageEvent) => {
      let env: StreamEnvelope;
      try {
        env = JSON.parse(e.data) as StreamEnvelope;
      } catch {
        return;
      }
      for (const sub of subsRef.current) {
        if (matchesFilter(env, sub.filter)) {
          sub.handler(env);
        }
      }
    };

    // The server sets event: <kind> on every frame (see writeStreamSSE).
    const kinds = [
      "system",
      "assistant",
      "tool_use",
      "tool_result",
      "result",
      "error",
      "user",
      "end",
      "push_state",
      "rehydrate_state",
      "label",
      "repo_summary_state",
      "tool_status",
      "watch_status",
      "signal_created",
      "item_captured",
      "ingest_run_started",
      "ingest_run_finished",
      "usage",
    ];
    for (const k of kinds) {
      es.addEventListener(k, dispatch as EventListener);
    }

    return () => {
      for (const k of kinds) {
        es.removeEventListener(k, dispatch as EventListener);
      }
      es.close();
      // Best-effort explicit close so the server frees the slot
      // immediately; if it doesn't land, the heartbeat reaps the
      // subscriber within 2s.
      void fetch(api.closeStreamURL(connToken), {
        method: "POST",
        credentials: "include",
        keepalive: true,
      }).catch(() => {
        /* non-fatal */
      });
    };
  }, []);

  return <Ctx.Provider value={ctxRef.current!}>{children}</Ctx.Provider>;
}

export function useStream(): StreamContext {
  const ctx = useContext(Ctx);
  if (!ctx) {
    throw new Error("useStream must be used inside <StreamProvider>");
  }
  return ctx;
}
