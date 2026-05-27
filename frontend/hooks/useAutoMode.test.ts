import { describe, it, expect } from "vitest";
import { renderHook } from "@testing-library/react";
import { useAutoMode } from "./useAutoMode";
import type { EventEnvelope } from "../lib/events";

function envState(phase: string): EventEnvelope {
  return {
    seq: 1,
    kind: "auto_mode_state",
    autoMode: { phase: phase as any },
    timestamp: "",
  } as any;
}

describe("useAutoMode", () => {
  it("derives canTakeOver=true while started", () => {
    const { result } = renderHook(() => useAutoMode([envState("started")]));
    expect(result.current.phase).toBe("started");
    expect(result.current.canTakeOver).toBe(true);
    expect(result.current.canResume).toBe(false);
  });

  it("flips on paused", () => {
    const { result } = renderHook(() =>
      useAutoMode([envState("started"), envState("paused")]),
    );
    expect(result.current.phase).toBe("paused");
    expect(result.current.canTakeOver).toBe(false);
    expect(result.current.canResume).toBe(true);
  });

  it("offers restart on aborted", () => {
    const { result } = renderHook(() => useAutoMode([envState("aborted")]));
    expect(result.current.canRestart).toBe(true);
  });

  it("returns disabled state for sessions with no auto_mode_state envelopes", () => {
    // Pre-T6 sessions never emitted auto_mode_state; the hook should
    // report a fully-off shape so the UI hides all auto-mode affordances.
    const { result } = renderHook(() => useAutoMode([]));
    expect(result.current.enabled).toBe(false);
    expect(result.current.phase).toBeNull();
    expect(result.current.canTakeOver).toBe(false);
    expect(result.current.canResume).toBe(false);
    expect(result.current.canRestart).toBe(false);
  });

  it("ignores warning-tagged auto_mode_state envelopes", () => {
    // The server emits warning=true envelopes for cap-approaching
    // notices; they don't change phase.
    const warning: EventEnvelope = {
      seq: 2,
      kind: "auto_mode_state",
      autoMode: { phase: "paused" as any, warning: true },
      timestamp: "",
    } as any;
    const { result } = renderHook(() =>
      useAutoMode([envState("started"), warning]),
    );
    expect(result.current.phase).toBe("started");
    expect(result.current.canTakeOver).toBe(true);
  });
});
