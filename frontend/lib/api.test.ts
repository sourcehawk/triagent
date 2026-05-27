import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import * as apiModule from "@/lib/api";
import { api } from "@/lib/api";

describe("auto-mode API helpers", () => {
  let fetchMock: ReturnType<typeof vi.fn>;
  beforeEach(() => {
    fetchMock = vi.fn().mockResolvedValue(
      new Response(`{"ok":true}`, { status: 200 }),
    );
    vi.stubGlobal("fetch", fetchMock);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("POSTs to /auto/takeover", async () => {
    await apiModule.takeover("inv-1");
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/investigations/inv-1/auto/takeover"),
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("POSTs to /auto/resume", async () => {
    await apiModule.resumeAuto("inv-1");
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/investigations/inv-1/auto/resume"),
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("restartAuto is an alias for resumeAuto", async () => {
    await apiModule.restartAuto("inv-1");
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/investigations/inv-1/auto/resume"),
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("throws on non-2xx", async () => {
    fetchMock.mockResolvedValueOnce(new Response("nope", { status: 500 }));
    await expect(apiModule.takeover("inv-1")).rejects.toThrow();
  });

  it("resumeAuto throws on non-2xx", async () => {
    fetchMock.mockResolvedValueOnce(new Response("nope", { status: 500 }));
    await expect(apiModule.resumeAuto("inv-1")).rejects.toThrow();
  });
});

describe("getProfileInputs", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("returns the inputs array", async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({
        inputs: [
          { id: "cluster_id", label: "Cluster", type: "cluster_id", optional: true },
        ],
      }),
    }) as unknown as typeof fetch;
    const res = await api.getProfileInputs();
    expect(res).toHaveLength(1);
    expect(res[0].id).toBe("cluster_id");
  });

  it("throws on non-ok response", async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 500,
      statusText: "Internal Server Error",
      json: async () => ({}),
    }) as unknown as typeof fetch;
    await expect(api.getProfileInputs()).rejects.toThrow();
  });

  it("calls GET /api/profile/inputs", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ inputs: [] }),
    });
    global.fetch = fetchMock as unknown as typeof fetch;
    await api.getProfileInputs();
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/profile/inputs",
      expect.objectContaining({ credentials: "include" }),
    );
  });
});
