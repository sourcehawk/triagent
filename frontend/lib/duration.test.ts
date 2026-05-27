import { describe, it, expect } from "vitest";
import { parseDuration, formatDuration } from "./duration";

describe("parseDuration", () => {
  it("parses bare seconds", () => {
    expect(parseDuration("300")).toBe(300);
    expect(parseDuration("0")).toBe(0);
  });

  it("parses single units", () => {
    expect(parseDuration("5m")).toBe(300);
    expect(parseDuration("1h")).toBe(3600);
    expect(parseDuration("2d")).toBe(172800);
    expect(parseDuration("45s")).toBe(45);
  });

  it("parses compound units", () => {
    expect(parseDuration("1h30m")).toBe(5400);
    expect(parseDuration("2h15m30s")).toBe(8130);
    expect(parseDuration("1d6h")).toBe(108000);
  });

  it("is case-insensitive and tolerant of whitespace", () => {
    expect(parseDuration("  5M  ")).toBe(300);
    expect(parseDuration("1H30M")).toBe(5400);
  });

  it("rejects garbage", () => {
    expect(parseDuration("")).toBe(null);
    expect(parseDuration("five minutes")).toBe(null);
    expect(parseDuration("30s10m")).toBe(null); // wrong order
    expect(parseDuration("1.5h")).toBe(null); // decimals unsupported
  });
});

describe("formatDuration", () => {
  it("round-trips with parseDuration for canonical forms", () => {
    for (const s of ["5m", "1h", "1h30m", "2d6h", "45s"]) {
      const n = parseDuration(s);
      expect(n).not.toBeNull();
      expect(formatDuration(n!)).toBe(s);
    }
  });

  it("handles zero and edge cases", () => {
    expect(formatDuration(0)).toBe("0s");
    expect(formatDuration(60)).toBe("1m");
    expect(formatDuration(3661)).toBe("1h1m1s");
  });
});
