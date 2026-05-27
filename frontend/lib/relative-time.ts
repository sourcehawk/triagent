// relativeTime renders an ISO-8601 timestamp as "5s ago" / "5m ago" /
// "2h ago" / "3d ago". Falls back to the raw string when parsing
// fails so the operator at least sees something usable.
//
// Used by the upstream-sync header strip and by the PR-state badges
// (so the tooltip on a "merged" pill can read "merged 3h ago").
export function relativeTime(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const seconds = Math.floor((Date.now() - t) / 1000);
  if (seconds < 60) return `${seconds}s ago`;
  const m = Math.floor(seconds / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h}h ago`;
  const d = Math.floor(h / 24);
  return `${d}d ago`;
}
