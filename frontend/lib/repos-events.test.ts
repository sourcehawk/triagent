/* @vitest-environment jsdom */
import { describe, it, expect, vi } from "vitest";
import { REPOS_CHANGED_EVENT, notifyReposChanged, onReposChanged } from "./repos-events";

describe("repos-events", () => {
  it("fires subscribers when notifyReposChanged is called", () => {
    const cb = vi.fn();
    const off = onReposChanged(cb);
    notifyReposChanged();
    expect(cb).toHaveBeenCalledTimes(1);
    off();
  });

  it("delivers to every subscriber so independent repo lists all refetch", () => {
    const a = vi.fn();
    const b = vi.fn();
    const offA = onReposChanged(a);
    const offB = onReposChanged(b);
    notifyReposChanged();
    expect(a).toHaveBeenCalledTimes(1);
    expect(b).toHaveBeenCalledTimes(1);
    offA();
    offB();
  });

  it("stops delivering after unsubscribe", () => {
    const cb = vi.fn();
    const off = onReposChanged(cb);
    off();
    notifyReposChanged();
    expect(cb).not.toHaveBeenCalled();
  });

  it("uses the triagent: DOM-event prefix (ADR-0005)", () => {
    expect(REPOS_CHANGED_EVENT).toBe("triagent:repos-changed");
  });
});
