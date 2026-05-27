import { describe, it, expect, vi, beforeEach } from "vitest";
import {
  filterChannels,
  sinceISOFromCreatedUnix,
  wikiChannelPrefix,
  _resetCacheForTests,
  type SlackChannelLite,
} from "./slackChannels";

const ch = (name: string, id = "C" + name): SlackChannelLite => ({ id, name });

describe("filterChannels", () => {
  it("returns all channels when query is empty or whitespace", () => {
    const list = [ch("incidents"), ch("oncall")];
    expect(filterChannels(list, "")).toEqual(list);
    expect(filterChannels(list, "   ")).toEqual(list);
  });

  it("matches case-insensitively against channel name", () => {
    const list = [ch("INCIDENTS"), ch("oncall")];
    const got = filterChannels(list, "inc");
    expect(got).toHaveLength(1);
    expect(got[0].name).toBe("INCIDENTS");
  });

  it("trims the query before matching", () => {
    const list = [ch("incidents")];
    expect(filterChannels(list, "  inc  ")).toHaveLength(1);
  });

  it("returns empty array when nothing matches", () => {
    const list = [ch("oncall"), ch("alerts")];
    expect(filterChannels(list, "incident")).toEqual([]);
  });
});

describe("sinceISOFromCreatedUnix", () => {
  it("formats a unix timestamp to a local datetime-local string", () => {
    const got = sinceISOFromCreatedUnix(1705321800);
    expect(got).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/);
  });

  it("returns empty string for zero or negative inputs", () => {
    expect(sinceISOFromCreatedUnix(0)).toBe("");
    expect(sinceISOFromCreatedUnix(-1)).toBe("");
  });
});

describe("wikiChannelPrefix", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    _resetCacheForTests();
  });

  it("fetches slack_channel_prefix from /api/connections", async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ slack_channel_prefix: "ir-" }),
    }) as unknown as typeof fetch;
    const p = await wikiChannelPrefix();
    expect(p).toBe("ir-");
  });

  it("caches subsequent calls — only one fetch", async () => {
    const spy = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ slack_channel_prefix: "incident-" }),
    });
    global.fetch = spy as unknown as typeof fetch;
    await wikiChannelPrefix();
    await wikiChannelPrefix();
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("returns empty string when field is absent", async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({}),
    }) as unknown as typeof fetch;
    const p = await wikiChannelPrefix();
    expect(p).toBe("");
  });
});
