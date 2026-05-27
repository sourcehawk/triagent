"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { watchesAPI, type Watch } from "@/lib/api";
import { useStream } from "@/lib/stream";

// useWatches returns the current list of watches plus an imperative
// refresh callback for mutations the SSE channel doesn't surface (e.g.
// PATCH /api/watches/{id} doesn't currently emit a watch_status event,
// so toggling Enabled needs to nudge the list manually).
export function useWatches(): { watches: Watch[] | null; refresh: () => void } {
  const [watches, setWatches] = useState<Watch[] | null>(null);
  const stream = useStream();
  const cancelledRef = useRef(false);

  const refresh = useCallback(() => {
    watchesAPI
      .list()
      .then((r) => {
        if (!cancelledRef.current) setWatches(r.watches);
      })
      .catch(() => {
        if (!cancelledRef.current) setWatches([]);
      });
  }, []);

  useEffect(() => {
    cancelledRef.current = false;
    refresh();
    const unsub = stream.subscribe({ scope: "global" }, (env) => {
      // Re-fetch on any event that could flip a watch's runtime state:
      // watch_status (enable/disable + poll status), and the ingestion-
      // run lifecycle events (so the spinner row updates within ms of
      // the agent spawning or returning).
      if (
        env.kind === "watch_status" ||
        env.kind === "ingest_run_started" ||
        env.kind === "ingest_run_finished"
      ) {
        refresh();
      }
    });
    return () => {
      cancelledRef.current = true;
      unsub();
    };
  }, [stream, refresh]);

  return { watches, refresh };
}
