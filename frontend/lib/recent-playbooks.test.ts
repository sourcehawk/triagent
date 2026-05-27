import { describe, it, expect, beforeEach, vi } from "vitest";
import {
  clearRecent,
  getRecent,
  MAX_ENTRIES,
  pushRecent,
  removeRecent,
} from "./recent-playbooks";

beforeEach(() => {
  // jsdom isn't enabled in this project's vitest config — sessionStorage
  // doesn't exist by default. Provide a tiny in-memory shim and stub the
  // window event dispatch so push/remove don't blow up.
  const store = new Map<string, string>();
  vi.stubGlobal("window", {
    sessionStorage: {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => { store.set(k, v); },
      removeItem: (k: string) => { store.delete(k); },
    },
    dispatchEvent: vi.fn(),
    CustomEvent,
  });
});

describe("recent-playbooks", () => {
  it("starts empty", () => {
    expect(getRecent()).toEqual([]);
  });

  it("push prepends and returns the new list", () => {
    expect(pushRecent("a")).toEqual(["a"]);
    expect(getRecent()).toEqual(["a"]);
    expect(pushRecent("b")).toEqual(["b", "a"]);
  });

  it("revisits move the id to the top without duplicating", () => {
    pushRecent("a");
    pushRecent("b");
    pushRecent("c");
    expect(pushRecent("a")).toEqual(["a", "c", "b"]);
  });

  it("caps the list at MAX_ENTRIES", () => {
    for (let i = 0; i < MAX_ENTRIES + 5; i++) pushRecent(`p${i}`);
    const got = getRecent();
    expect(got.length).toBe(MAX_ENTRIES);
    expect(got[0]).toBe(`p${MAX_ENTRIES + 4}`);
    // The oldest five (p0..p4) should be evicted.
    expect(got).not.toContain("p0");
    expect(got).not.toContain("p4");
  });

  it("ignores empty ids", () => {
    pushRecent("a");
    expect(pushRecent("")).toEqual(["a"]);
  });

  it("removeRecent drops the id and leaves order intact", () => {
    pushRecent("a");
    pushRecent("b");
    pushRecent("c");
    expect(removeRecent("b")).toEqual(["c", "a"]);
  });

  it("removeRecent is a no-op when the id isn't present", () => {
    pushRecent("a");
    expect(removeRecent("x")).toEqual(["a"]);
  });

  it("clearRecent empties the list", () => {
    pushRecent("a");
    pushRecent("b");
    clearRecent();
    expect(getRecent()).toEqual([]);
  });
});
