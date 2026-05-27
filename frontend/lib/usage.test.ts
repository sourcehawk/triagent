/* @vitest-environment node */
import { describe, expect, test } from "vitest";
import { formatTokens, formatCostUSD, totalTokens, type UsageLike } from "./usage";

describe("totalTokens", () => {
  test("sums every component", () => {
    const u: UsageLike = {
      inputTokens: 100,
      outputTokens: 200,
      cacheCreationInputTokens: 300,
      cacheReadInputTokens: 400,
    };
    expect(totalTokens(u)).toBe(1000);
  });
  test("treats missing components as zero", () => {
    expect(totalTokens({ inputTokens: 10 })).toBe(10);
    expect(totalTokens(undefined)).toBe(0);
    expect(totalTokens(null)).toBe(0);
  });
});

describe("formatTokens", () => {
  test("renders under 1k unchanged", () => {
    expect(formatTokens(0)).toBe("0");
    expect(formatTokens(42)).toBe("42");
    expect(formatTokens(999)).toBe("999");
  });
  test("renders 1k–1m with one decimal of k", () => {
    expect(formatTokens(1000)).toBe("1.0k");
    expect(formatTokens(12300)).toBe("12.3k");
    expect(formatTokens(999_400)).toBe("999.4k");
  });
  test("renders >=1m with one decimal of m", () => {
    expect(formatTokens(1_000_000)).toBe("1.0m");
    expect(formatTokens(2_345_000)).toBe("2.3m");
  });
});

describe("formatCostUSD", () => {
  test("renders <$1 in cents-style decimals", () => {
    expect(formatCostUSD(0)).toBe("$0.00");
    expect(formatCostUSD(0.0042)).toBe("$0.0042");
    expect(formatCostUSD(0.12)).toBe("$0.12");
  });
  test("renders >=$1 with two decimals", () => {
    expect(formatCostUSD(1.234)).toBe("$1.23");
    expect(formatCostUSD(45.6)).toBe("$45.60");
  });
});
