// parseDuration accepts a human-readable interval like "5m", "1h",
// "1h30m", "2d6h" and returns the total seconds. A pure-digit input
// ("300") is interpreted as seconds for backward compatibility. Returns
// null when the input can't be parsed.
//
// Supported units: d, h, m, s. Each unit may appear at most once and
// must be in descending order of size (d before h before m before s).
// Decimals are not supported — the operator picks the smallest
// unit that gives them the precision they want.
export function parseDuration(s: string): number | null {
  const trimmed = s.trim().toLowerCase();
  if (!trimmed) return null;
  if (/^\d+$/.test(trimmed)) return Number(trimmed);
  const re = /^(?:(\d+)d)?(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?$/;
  const m = re.exec(trimmed);
  if (!m) return null;
  const [, d, h, min, sec] = m;
  if (!d && !h && !min && !sec) return null;
  return (
    Number(d ?? 0) * 86400 +
    Number(h ?? 0) * 3600 +
    Number(min ?? 0) * 60 +
    Number(sec ?? 0)
  );
}

// formatDuration converts a seconds count to the shortest compound
// d/h/m/s string. 0 → "0s"; 3600 → "1h"; 5400 → "1h30m".
export function formatDuration(total: number): string {
  if (total <= 0) return "0s";
  let n = Math.floor(total);
  const d = Math.floor(n / 86400);
  n %= 86400;
  const h = Math.floor(n / 3600);
  n %= 3600;
  const m = Math.floor(n / 60);
  const s = n % 60;
  const parts: string[] = [];
  if (d) parts.push(`${d}d`);
  if (h) parts.push(`${h}h`);
  if (m) parts.push(`${m}m`);
  if (s) parts.push(`${s}s`);
  return parts.join("") || "0s";
}
