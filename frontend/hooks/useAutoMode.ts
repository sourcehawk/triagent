"use client";

import { useMemo } from "react";

import type { AutoModePhase, EventEnvelope } from "../lib/events";

// AutoModeUIState is the derived view the auto-mode UI affordances read
// from. The hook folds the auto_mode_state envelope stream into a small
// flag bag so components don't have to re-scan events themselves.
export type AutoModeUIState = {
  // enabled is true once the session has emitted at least one
  // auto_mode_state envelope. Pre-T6 sessions stay disabled forever.
  enabled: boolean;
  // phase mirrors the latest non-warning auto_mode_state.phase value
  // the server has emitted, or null if there is none.
  phase: AutoModePhase | null;
  // canTakeOver is true while the operator is actively driving the
  // session (started / resumed) — the UI exposes a "take over" button.
  canTakeOver: boolean;
  // canResume is true while the operator is paused after a human
  // takeover — the UI exposes a "resume auto-operator" button.
  canResume: boolean;
  // canRestart is true once the operator has finished or aborted —
  // the UI exposes a "restart auto-operator" button.
  canRestart: boolean;
  // lastReason carries the reason text from the most recent non-warning
  // auto_mode_state envelope, if any. The finished/aborted banner uses
  // this to surface the operator agent's closing rationale.
  lastReason: string | null;
};

// useAutoMode folds an event stream into the auto-mode UI flag bag.
//
// It is a "hook" by convention only — the body is pure — but the
// useMemo wrapping keeps the result stable across renders when the
// events array reference hasn't changed, which matters for the
// downstream buttons that subscribe to specific flags.
//
// Warning-tagged envelopes (autoMode.warning === true) are notices
// emitted by the server (e.g. wake-cap approaching) that don't change
// phase; the hook skips them so a warning at "paused" doesn't bleed
// into the canTakeOver/canResume flags.
export function useAutoMode(events: EventEnvelope[]): AutoModeUIState {
  return useMemo(() => {
    let phase: AutoModePhase | null = null;
    let lastReason: string | null = null;
    for (const e of events) {
      if (e.kind === "auto_mode_state" && e.autoMode?.warning !== true) {
        phase = e.autoMode!.phase;
        lastReason = e.autoMode!.reason ?? e.autoMode!.error ?? null;
      }
    }
    const active = phase === "started" || phase === "resumed";
    return {
      enabled: phase !== null,
      phase,
      canTakeOver: active,
      canResume: phase === "paused",
      canRestart: phase === "finished" || phase === "aborted",
      lastReason,
    };
  }, [events]);
}
