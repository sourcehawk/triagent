// Pure helpers used by the SlackChannelPicker and the wiki modal. Lives
// in lib/ (not components/) so vitest's node-only environment can exercise
// the logic without DOM setup.

export type SlackChannelLite = {
  id: string;
  name: string;
  // CreatedUnix from the backend channelDTO; undefined for legacy clients.
  created_unix?: number;
};

// filterChannels narrows a channel list against a free-form query.
// Case-insensitive, trims whitespace, matches against channel name.
export function filterChannels<T extends { name: string }>(
  channels: readonly T[],
  query: string,
): T[] {
  const q = query.trim().toLowerCase();
  if (q === "") return channels.slice();
  return channels.filter((c) => c.name.toLowerCase().includes(q));
}

// sinceISOFromCreatedUnix converts a Slack `created` unix timestamp to a
// datetime-local string suitable for an <input type="datetime-local">.
// Returns empty string for non-positive inputs so callers can `||` to a
// default.
export function sinceISOFromCreatedUnix(unix: number): string {
  if (!Number.isFinite(unix) || unix <= 0) return "";
  const d = new Date(unix * 1000);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// wikiChannelPrefix fetches the slack_channel_prefix from the server's
// /api/connections boot-config response (sourced from the loaded profile).
// Cached in-module after the first successful fetch; multiple concurrent
// callers share one in-flight promise.
let cachedPrefix: string | null = null;
let inflight: Promise<string> | null = null;

export async function wikiChannelPrefix(): Promise<string> {
  if (cachedPrefix !== null) return cachedPrefix;
  if (inflight) return inflight;
  inflight = fetch("/api/connections")
    .then((r) =>
      r.ok
        ? r.json()
        : Promise.reject(new Error(`prefix fetch: ${r.status}`)),
    )
    .then((body: { slack_channel_prefix?: string }) => {
      const p = body.slack_channel_prefix ?? "";
      cachedPrefix = p;
      return p;
    })
    .finally(() => {
      inflight = null;
    });
  return inflight;
}

// _resetCacheForTests clears the in-module cache so unit tests start clean.
export function _resetCacheForTests(): void {
  cachedPrefix = null;
  inflight = null;
}
