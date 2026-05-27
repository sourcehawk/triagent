"use client";

import { useEffect, useReducer, useState } from "react";
import { api, ApiError, type Capabilities, type Investigation, type StreamEnvelope } from "@/lib/api";
import {
  applyEvent,
  type EventEnvelope,
  type SessionStatus,
  type TranscriptItem,
} from "@/lib/events";
import { useStream } from "@/lib/stream";
import { ActivityPanel, ActivityRail } from "./ActivityPanel";
import { SessionView } from "./SessionView";

type Props = {
  investigation: Investigation;
  // onReset is kept for API compatibility with the route's client (it
  // routes the user back to a fresh /investigations/new). SessionView
  // no longer wires its own "new investigation" button — the back link
  // in the header takes the operator to the investigations home.
  onReset?: () => void;
};

const ACTIVITY_VISIBLE_KEY = "triagent.activity.visible";

function readActivityVisible(): boolean {
  if (typeof window === "undefined") return true;
  const v = window.localStorage.getItem(ACTIVITY_VISIBLE_KEY);
  return v === null ? true : v === "1";
}

// SessionWorkspace owns the per-investigation streaming state — the SSE
// connection, the transcript reducer, the session status — so the chat
// (SessionView) and the activity feed (ActivityPanel) can render off
// the same source of truth.
export function SessionWorkspace({ investigation: initialInv }: Props) {
  const [investigation, setInvestigation] = useState<Investigation>(initialInv);
  const [items, dispatch] = useReducer(applyEvent, [] as TranscriptItem[]);
  // Raw event stream held alongside the reduced transcript. The
  // reducer deliberately drops auto_mode_state envelopes (they don't
  // render as chat items) so SessionView's useAutoMode hook needs the
  // unreduced list. Appending strictly mirrors dispatch — both grow
  // from snapshot first, then live envelopes — so the indices line up.
  const [events, setEvents] = useState<EventEnvelope[]>([]);
  // "starting" is the spawning-claude-for-the-first-time state. Archived
  // sessions are read-only and idle by definition. Sessions whose
  // .started bit is already true came back from disk in a state where
  // claude was up in some prior launcher process — the rehydrate path
  // (triggered on the next follow-up) is what brings the live wiring
  // back; until then the operator just reads the transcript, so "idle"
  // is the honest state. Showing "starting" for those would leave the
  // "spawning claude…" spinner pinned even though nothing is starting.
  //
  // `streaming` from the server takes precedence — if the agent was
  // mid-run when the route remounted (operator tabbed away and back),
  // seed straight into "streaming" so the working-spinner shows
  // immediately. Without this, status sits at "idle" until the next
  // live envelope arrives, which during a long tool call can take a
  // while and reads as "agent stopped".
  const [status, setStatus] = useState<SessionStatus>(() => {
    if (initialInv.archived) return "idle";
    if (initialInv.streaming) return "streaming";
    if (initialInv.started) return "idle";
    return "starting";
  });
  const [streamErr, setStreamErr] = useState<string | null>(null);
  const [activityVisible, setActivityVisible] = useState<boolean>(readActivityVisible);
  const [capabilities, setCapabilities] = useState<Capabilities | null>(null);
  const [lastPushState, setLastPushState] = useState<NonNullable<EventEnvelope["pushState"]> | undefined>(undefined);
  const [lastRehydrateState, setLastRehydrateState] = useState<NonNullable<EventEnvelope["rehydrateState"]> | undefined>(undefined);
  const stream = useStream();

  // Fetch capabilities once on mount. Used by SessionView to gate the
  // "promote to wiki" button and surface the reason it's disabled.
  useEffect(() => {
    api.getCapabilities().then(setCapabilities).catch(() => {
      /* non-fatal — button just stays disabled */
    });
  }, []);

  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem(ACTIVITY_VISIBLE_KEY, activityVisible ? "1" : "0");
  }, [activityVisible]);

  // Kick off the claude session once. Skip for:
  // - archived investigations (original process is gone, the port-forward
  //   is gone, the server returns "investigation is archived" anyway);
  // - investigations whose .started bit is already true, i.e. restored
  //   from disk after a launcher restart — the server would just return
  //   "session already started" and the rehydrate path handles the live
  //   wiring on the first follow-up. Calling startSession here is a
  //   wasted round-trip that also contributes to the per-origin
  //   connection pool pressure during tab-switch bursts.
  useEffect(() => {
    if (investigation.archived || investigation.started) return;
    let cancelled = false;
    api.startSession(investigation.id).catch((e) => {
      if (cancelled) return;
      if (e instanceof ApiError && /already started/i.test(e.message)) return;
      setStreamErr(e instanceof ApiError ? e.message : String(e));
      setStatus("error");
    });
    return () => {
      cancelled = true;
    };
  }, [investigation.id, investigation.archived, investigation.started]);

  // Subscribe to the multiplex stream for this investigation. On mount:
  // 1. Subscribe synchronously so live envelopes are buffered immediately.
  // 2. Fetch the transcript snapshot to seed the reducer.
  // 3. Apply the snapshot, then drain buffered live envelopes (seq > lastSeq).
  // This subscribe-then-snapshot ordering avoids missing events that arrive
  // between when the snapshot was taken and when we subscribe.
  useEffect(() => {
    let cancelled = false;
    let lastSeq = 0;
    const buffered: StreamEnvelope[] = [];
    let snapshotApplied = false;

    // Subscribe synchronously so envelopes arriving during the
    // snapshot fetch land in `buffered`. After the snapshot is
    // applied, we drain `buffered` skipping seq <= lastSeq.
    const unsub = stream.subscribe(
      { scope: "investigation", id: investigation.id },
      (env) => {
        if (cancelled) return;
        if (!snapshotApplied) {
          buffered.push(env);
          return;
        }
        if (env.seq <= lastSeq) return;
        applyEnvelope(env);
      },
    );

    void api
      .getInvestigationTranscript(investigation.id)
      .then((res) => {
        if (cancelled) return;
        lastSeq = res.lastSeq;
        // Walk the snapshot to derive the terminal status — mirrors
        // applyEnvelope's per-kind transitions, but the dispatch loop
        // below only feeds the reducer (not the status setter), so
        // without this pass a remount onto a still-streaming session
        // would freeze at whatever initialInv.streaming said at fetch
        // time and miss any "end" that landed since.
        let derivedStatus: SessionStatus | undefined;
        for (const ev of res.events) {
          dispatch(ev);
          if (ev.kind === "result" || ev.kind === "end") {
            derivedStatus = "idle";
          } else if (
            !investigation.archived &&
            (ev.kind === "user" ||
              (ev.kind === "system" && ev.subtype === "init"))
          ) {
            derivedStatus = "streaming";
          }
        }
        if (derivedStatus !== undefined) setStatus(derivedStatus);
        // Seed the raw events buffer with the snapshot in one batch so
        // useAutoMode's useMemo only fires once per resume rather than
        // once per replayed envelope. SessionWorkspace is keyed by
        // investigation.id at the route level so the buffer is always
        // empty when this fires for the current id — append-as-replace
        // is safe.
        setEvents(res.events);
        snapshotApplied = true;
        // Drain buffered live envelopes that arrived during the fetch.
        for (const env of buffered) {
          if (env.seq > lastSeq) applyEnvelope(env);
        }
      })
      .catch((e) => {
        if (cancelled) return;
        setStreamErr(e instanceof ApiError ? e.message : String(e));
        setStatus("error");
      });

    function applyEnvelope(env: StreamEnvelope) {
      // Convert the multiplex envelope into the existing
      // EventEnvelope shape the reducer expects. Field set is a
      // strict superset; the cast is safe.
      const e = env as unknown as EventEnvelope;
      dispatch(e);
      setEvents((prev) => [...prev, e]);

      // Preserve the existing per-kind side-effects.
      if (env.kind === "user" && !investigation.archived) {
        setStatus("streaming");
      } else if (env.kind === "result" || env.kind === "end") {
        setStatus("idle");
      } else if (env.kind === "system" && env.subtype === "init" && !investigation.archived) {
        setStatus("streaming");
      } else if (env.kind === "push_state" && env.pushState) {
        const ps = env.pushState;
        setInvestigation((prev) => {
          if (!prev) return prev;
          if (ps.phase === "pushing") {
            return { ...prev, pushInProgress: true, pushStartedAt: ps.startedAt, pushError: undefined };
          }
          if (ps.phase === "success") {
            return {
              ...prev,
              pushInProgress: false,
              pushStartedAt: undefined,
              pushError: undefined,
              pushed: true,
              pushUrl: ps.url ?? prev.pushUrl,
              prState: "open",
            };
          }
          return { ...prev, pushInProgress: false, pushStartedAt: undefined, pushError: ps.error };
        });
        setLastPushState(ps);
      } else if (env.kind === "rehydrate_state" && env.rehydrateState) {
        setLastRehydrateState(env.rehydrateState);
        // On success, refetch the investigation so `resumable` flips to
        // false (the server cleared needsRehydrate as part of rehydrate).
        if (env.rehydrateState.phase === "succeeded") {
          void api.getInvestigation(investigation.id).then(setInvestigation).catch(() => {
            /* non-fatal */
          });
        }
      } else if (env.kind === "usage" || env.kind === "result") {
        // Fold the per-API-call usage (from `usage` envelopes) and the
        // invocation cost (from `result` envelopes) into the investigation
        // snapshot so the SessionView footer + sidebar tick live without
        // waiting for /api/investigations to refresh. Mirrors the
        // server-side accumulator in investigate/internal/server/manager.go's
        // publish(). The split exists because claude's result.usage is the
        // LAST API call's snapshot, not the invocation total — summing it
        // under-reports dramatically. `usage` envelopes carry the per-call
        // truth; `result` envelopes carry the cost roll-up.
        const u = env.kind === "usage" ? env.usage : undefined;
        const cost = env.kind === "result" ? env.costUsd ?? 0 : 0;
        if (u || cost) {
          setInvestigation((prev) => {
            if (!prev) return prev;
            const cur = prev.usage ?? {};
            const merged = {
              inputTokens: (cur.inputTokens ?? 0) + (u?.inputTokens ?? 0),
              outputTokens: (cur.outputTokens ?? 0) + (u?.outputTokens ?? 0),
              cacheCreationInputTokens:
                (cur.cacheCreationInputTokens ?? 0) +
                (u?.cacheCreationInputTokens ?? 0),
              cacheReadInputTokens:
                (cur.cacheReadInputTokens ?? 0) + (u?.cacheReadInputTokens ?? 0),
            };
            const next = {
              ...prev,
              usage: merged,
              costUsd: (prev.costUsd ?? 0) + cost,
            };
            // Mirror updated totals into the sidebar list so the row
            // ticks alongside the open SessionView.
            if (typeof window !== "undefined") {
              window.dispatchEvent(
                new CustomEvent("triagent:investigation-changed", {
                  detail: next,
                }),
              );
            }
            return next;
          });
        }
      } else if (env.kind === "label") {
        // When the agent calls set_session_label, re-fetch the investigation
        // snapshot so the header and the sidebar both reflect the new label.
        void api
          .getInvestigation(investigation.id)
          .then((updated) => {
            setInvestigation(updated);
            if (typeof window !== "undefined") {
              window.dispatchEvent(
                new CustomEvent("triagent:investigation-changed", {
                  detail: updated,
                }),
              );
            }
          })
          .catch(() => {
            /* non-fatal */
          });
      }
    }

    return () => {
      cancelled = true;
      unsub();
    };
  }, [investigation.id, investigation.archived, stream]);

  return (
    <div className="flex h-full">
      <div className="flex-1 min-w-0 px-6 py-6">
        <SessionView
          investigation={investigation}
          items={items}
          events={events}
          status={status}
          streamErr={streamErr}
          capabilities={capabilities}
          onUpdated={setInvestigation}
          serverPushState={lastPushState}
          onServerPushStateConsumed={() => setLastPushState(undefined)}
          serverRehydrateState={lastRehydrateState}
          onServerRehydrateStateConsumed={() => setLastRehydrateState(undefined)}
        />
      </div>
      {activityVisible ? (
        <ActivityPanel
          investigation={investigation}
          items={items}
          onCollapse={() => setActivityVisible(false)}
        />
      ) : (
        <ActivityRail onExpand={() => setActivityVisible(true)} />
      )}
    </div>
  );
}
