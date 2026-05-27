import { render, screen, waitFor, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { Sidebar } from "./Sidebar";
import { api, type Investigation, type StreamEnvelope } from "@/lib/api";
import { DialogProvider } from "@/lib/dialog";
import type { StreamFilter } from "@/lib/stream-dispatch";

// Capture subscribed handlers so tests can synthesize stream envelopes.
type Handler = (env: StreamEnvelope) => void;
const subscribers: { filter: StreamFilter; handler: Handler }[] = [];
vi.mock("@/lib/stream", () => ({
  useStream: () => ({
    subscribe: (filter: StreamFilter, handler: Handler) => {
      const entry = { filter, handler };
      subscribers.push(entry);
      return () => {
        const idx = subscribers.indexOf(entry);
        if (idx >= 0) subscribers.splice(idx, 1);
      };
    },
  }),
}));
function emit(env: StreamEnvelope) {
  for (const s of subscribers) s.handler(env);
}

// Sidebar uses next/navigation hooks; stub the module so jsdom doesn't
// blow up trying to reach the App Router context.
vi.mock("next/navigation", () => ({
  usePathname: () => "/investigations",
  useSearchParams: () => new URLSearchParams(),
}));

function makeInvestigation(overrides: Partial<Investigation> = {}): Investigation {
  return {
    id: "x",
    namespace: "ns",
    mcpConfigPath: "/tmp/mcp.json",
    sessionDir: "/tmp/session",
    promEnabled: false,
    createdAt: "2026-05-11T00:00:00Z",
    started: false,
    streaming: false,
    archived: false,
    resumable: false,
    syncState: { status: "synced" },
    ...overrides,
  } as Investigation;
}

beforeEach(() => {
  subscribers.length = 0;
  vi.restoreAllMocks();
  // jsdom in this project doesn't ship a working localStorage by
  // default; child panels (LinkedReposPanel, ConnectionsPanel) reach
  // for it on mount to restore their collapsed state. Stub it so the
  // surrounding Sidebar tree renders cleanly.
  const store = new Map<string, string>();
  Object.defineProperty(window, "localStorage", {
    configurable: true,
    value: {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => void store.set(k, v),
      removeItem: (k: string) => void store.delete(k),
      clear: () => store.clear(),
      key: () => null,
      length: 0,
    },
  });
  // Stub network surfaces that Sidebar's child panels poll on mount
  // (ConnectionsPanel hits /api/connections, LinkedReposPanel hits
  // /api/repos). Tests not exercising those need them to resolve
  // silently rather than reject with "network error".
  vi.spyOn(api, "getConnections").mockResolvedValue({
    slack: false,
    incidentio: false,
    slack_channel_prefix: "",
  });
});

describe("Sidebar auto-mode glyph", () => {
  it("shows bot glyph for auto-mode investigations", async () => {
    vi.spyOn(api, "listInvestigations").mockResolvedValue([
      makeInvestigation({
        id: "x",
        label: "A test",
        auto: { enabled: true, phase: "started" },
      }),
    ]);
    render(
      <DialogProvider>
        <Sidebar activeId="x" onSelect={() => {}} onNew={() => {}} />
      </DialogProvider>,
    );
    await waitFor(() =>
      expect(screen.getByTestId("auto-glyph")).toBeInTheDocument(),
    );
  });

  it("does not show bot glyph when auto-mode is disabled", async () => {
    vi.spyOn(api, "listInvestigations").mockResolvedValue([
      makeInvestigation({ id: "y", label: "no-auto" }),
    ]);
    render(
      <DialogProvider>
        <Sidebar activeId="y" onSelect={() => {}} onNew={() => {}} />
      </DialogProvider>,
    );
    await waitFor(() => expect(screen.getByText("no-auto")).toBeInTheDocument());
    expect(screen.queryByTestId("auto-glyph")).toBeNull();
  });
});

describe("Sidebar usage readout", () => {
  it("shows token + cost total on the StatusLine when usage is populated", async () => {
    vi.spyOn(api, "listInvestigations").mockResolvedValue([
      makeInvestigation({
        id: "u",
        label: "spent-some",
        usage: {
          inputTokens: 5_000,
          outputTokens: 7_300,
          cacheReadInputTokens: 200,
        },
        costUsd: 1.234,
      }),
    ]);
    render(
      <DialogProvider>
        <Sidebar activeId="u" onSelect={() => {}} onNew={() => {}} />
      </DialogProvider>,
    );
    await waitFor(() =>
      expect(screen.getByText("spent-some")).toBeInTheDocument(),
    );
    // 5000 + 7300 + 200 = 12500 → "12.5k tok"; 1.234 → "$1.23"
    expect(screen.getByText(/12\.5k tok/)).toBeInTheDocument();
    expect(screen.getByText(/\$1\.23/)).toBeInTheDocument();
  });

  it("omits the readout when usage is absent (fresh session)", async () => {
    vi.spyOn(api, "listInvestigations").mockResolvedValue([
      makeInvestigation({ id: "fresh", label: "fresh-session" }),
    ]);
    render(
      <DialogProvider>
        <Sidebar activeId="fresh" onSelect={() => {}} onNew={() => {}} />
      </DialogProvider>,
    );
    await waitFor(() =>
      expect(screen.getByText("fresh-session")).toBeInTheDocument(),
    );
    expect(screen.queryByText(/tok/)).toBeNull();
  });

  it("ticks live when a result envelope carrying usage lands on the multiplex stream", async () => {
    vi.spyOn(api, "listInvestigations").mockResolvedValue([
      makeInvestigation({ id: "live", label: "ticking" }),
    ]);
    render(
      <DialogProvider>
        <Sidebar activeId="live" onSelect={() => {}} onNew={() => {}} />
      </DialogProvider>,
    );
    await waitFor(() =>
      expect(screen.getByText("ticking")).toBeInTheDocument(),
    );
    expect(screen.queryByText(/tok/)).toBeNull();

    act(() => {
      emit({
        seq: 1,
        kind: "result",
        timestamp: "2026-05-13T22:00:00Z",
        investigationId: "live",
        usage: { inputTokens: 1234, outputTokens: 2000 },
        costUsd: 0.42,
      });
    });
    await waitFor(() =>
      expect(screen.getByText(/3\.2k tok/)).toBeInTheDocument(),
    );
    expect(screen.getByText(/\$0\.42/)).toBeInTheDocument();
  });
});

describe("Sidebar live streaming status", () => {
  it("flips status to streaming on stream activity and back to idle on end", async () => {
    vi.spyOn(api, "listInvestigations").mockResolvedValue([
      makeInvestigation({ id: "x", label: "auto-spawned", started: false, streaming: false }),
    ]);
    render(
      <DialogProvider>
        <Sidebar activeId={null} onSelect={() => {}} onNew={() => {}} />
      </DialogProvider>,
    );
    await waitFor(() => expect(screen.getByText("auto-spawned")).toBeInTheDocument());
    // Initially the row should show "ready" (started=false, streaming=false).
    expect(screen.getByText("ready")).toBeInTheDocument();

    // An assistant event lands for this investigation — sidebar should
    // flip to streaming without anyone opening the session.
    act(() => {
      emit({ seq: 5, kind: "assistant", timestamp: "2026-05-13T22:00:00Z", investigationId: "x" });
    });
    await waitFor(() => expect(screen.getByText("streaming")).toBeInTheDocument());

    // `end` envelope: streaming → false, started stays true (an end has
    // happened, so the row is "idle" now).
    act(() => {
      emit({ seq: 6, kind: "end", timestamp: "2026-05-13T22:00:01Z", investigationId: "x" });
    });
    await waitFor(() => expect(screen.getByText("idle")).toBeInTheDocument());
  });
});
