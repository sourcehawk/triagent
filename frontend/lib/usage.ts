// Helpers for rendering claude token usage + cost in the sidebar and
// session footer. UsageLike mirrors the wire-shape Usage (events.ts /
// claude.Usage in Go) but keeps every field optional so callers can
// pass partial blobs without spreading defaults at the call site.

export type UsageLike = {
  inputTokens?: number;
  outputTokens?: number;
  cacheCreationInputTokens?: number;
  cacheReadInputTokens?: number;
} | null | undefined;

// totalTokens sums every component of the usage blob, treating missing
// fields as zero. Used for the "throughput" number the UI shows —
// distinct from cost, which reflects the per-class pricing.
export function totalTokens(u: UsageLike): number {
  if (!u) return 0;
  return (
    (u.inputTokens ?? 0) +
    (u.outputTokens ?? 0) +
    (u.cacheCreationInputTokens ?? 0) +
    (u.cacheReadInputTokens ?? 0)
  );
}

// formatTokens picks a unit (raw / k / m) based on magnitude. Mirrors
// the abbreviation styles in the dashboard cards: bare digits under 1k,
// one decimal of "k" through ~999.9k, one decimal of "m" past 1m.
export function formatTokens(n: number): string {
  if (n < 1000) return String(n);
  if (n < 1_000_000) return `${(n / 1000).toFixed(1)}k`;
  return `${(n / 1_000_000).toFixed(1)}m`;
}

// formatCostUSD renders a dollar amount with enough precision to be
// meaningful at every scale. Sub-cent amounts get four decimals so
// the operator can see fractions of a cent on cheap turns; >= $1
// rounds to the nearest cent like a normal money number.
export function formatCostUSD(usd: number): string {
  // Use four-decimal precision only for non-zero amounts below one
  // cent — at exactly zero (no result events yet) the operator wants
  // the simpler "$0.00" rather than four decimals of nothing.
  if (usd > 0 && usd < 0.01) return `$${usd.toFixed(4)}`;
  return `$${usd.toFixed(2)}`;
}
