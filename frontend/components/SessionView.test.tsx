import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { SessionView } from "./SessionView";
import { DialogProvider } from "@/lib/dialog";
import type { Investigation } from "@/lib/api";
import { reduce, type EventEnvelope } from "@/lib/events";

// Minimal Investigation stub for SessionView. Only fields the component
// reads on render need real values; everything else can be a default
// (empty string / false / undefined) without affecting the gated UI
// affordances we're testing here.
function makeInvestigation(overrides: Partial<Investigation> = {}): Investigation {
  return {
    id: "inv-test",
    namespace: "default",
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

// Helper to build a minimal auto_mode_state envelope. Cast as any so
// the test doesn't have to satisfy every optional field on EventEnvelope.
function autoModeEvent(
  phase: "started" | "paused" | "resumed" | "finished" | "aborted",
  extra: Record<string, unknown> = {},
): EventEnvelope {
  return {
    seq: 1,
    kind: "auto_mode_state",
    autoMode: { phase, ...extra },
    timestamp: "",
  } as unknown as EventEnvelope;
}

function renderSessionView(events: EventEnvelope[]) {
  // DialogProvider is required because SessionView calls useDialog().
  return render(
    <DialogProvider>
      <SessionView
        investigation={makeInvestigation()}
        items={reduce(events)}
        events={events}
        status="idle"
        streamErr={null}
        capabilities={null}
      />
    </DialogProvider>,
  );
}

describe("SessionView auto-mode gating", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("disables composer and shows Take over while auto is started", () => {
    renderSessionView([autoModeEvent("started")]);
    expect(
      screen.getByPlaceholderText(/Auto mode is active/i),
    ).toBeDisabled();
    // Composer chip — exact aria-label "Take over" — distinct from the
    // banner's "Take over auto mode" so both can coexist.
    expect(
      screen.getByRole("button", { name: "Take over" }),
    ).toBeInTheDocument();
  });

  it("enables composer and shows Resume after paused", () => {
    renderSessionView([autoModeEvent("paused")]);
    expect(
      screen.queryByPlaceholderText(/Auto mode is active/i),
    ).toBeNull();
    expect(
      screen.getByRole("button", { name: /Resume auto mode/i }),
    ).toBeInTheDocument();
  });

  it("offers Restart auto mode when aborted", () => {
    renderSessionView([autoModeEvent("aborted", { error: "x" })]);
    // The aborted banner AND the composer chip both surface "Restart
    // auto mode" — getAllByRole captures both. We only care that the
    // affordance exists.
    expect(
      screen.getAllByRole("button", { name: /Restart auto mode/i }).length,
    ).toBeGreaterThan(0);
  });

  it("renders the finished banner with the closing reason", () => {
    renderSessionView([
      autoModeEvent("finished", { reason: "wiki proposal staged" }),
    ]);
    const banner = screen.getByRole("region", { name: /Auto mode finished/i });
    expect(banner).toBeInTheDocument();
    expect(banner).toHaveTextContent(/wiki proposal staged/i);
  });
});

describe("SessionView transcript styling", () => {
  it("styles operator user envelope with pink class", () => {
    renderSessionView([
      {
        seq: 1,
        kind: "user",
        origin: "operator",
        text: "wiki",
        timestamp: "",
      } as unknown as EventEnvelope,
    ]);
    const bubble = screen.getByText("wiki").closest("[data-testid='user-envelope']");
    expect(bubble?.className).toMatch(/border-l-pink|border-auto-operator|border-pink-500/);
  });

  it("labels operator user envelope as Auto Operator", () => {
    renderSessionView([
      {
        seq: 1,
        kind: "user",
        origin: "operator",
        text: "wiki",
        timestamp: "",
      } as unknown as EventEnvelope,
    ]);
    expect(screen.getByText(/Auto Operator/)).toBeInTheDocument();
  });

  it("renders auto_mode_state as divider chip", () => {
    renderSessionView([
      {
        seq: 1,
        kind: "auto_mode_state",
        autoMode: { phase: "started" },
        timestamp: "",
      } as unknown as EventEnvelope,
    ]);
    expect(screen.getByText(/auto mode started/i)).toBeInTheDocument();
  });
});

describe("SessionView auto-mode banner", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("shows the auto-mode banner while phase is started", () => {
    renderSessionView([autoModeEvent("started")]);
    expect(
      screen.getByText(/This session is running in auto mode/i),
    ).toBeInTheDocument();
  });

  it("hides the banner once paused", () => {
    renderSessionView([
      autoModeEvent("started"),
      { ...autoModeEvent("paused"), seq: 2 } as EventEnvelope,
    ]);
    expect(
      screen.queryByText(/This session is running in auto mode/i),
    ).toBeNull();
  });

  it("clicking the banner calls takeover", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(new Response(`{"ok":true}`, { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    renderSessionView([autoModeEvent("started")]);
    fireEvent.click(
      screen.getByRole("button", { name: /Take over auto mode/i }),
    );
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("/api/investigations/"),
        expect.objectContaining({ method: "POST" }),
      ),
    );
    vi.unstubAllGlobals();
  });
});

describe("SessionView archived push affordance", () => {
  // Renders an archived investigation directly (no events flow). The
  // archived branch is the only place the push-to-upstream button is
  // gated on syncState + pushError, so these tests don't need useAutoMode.
  function renderArchived(inv: Investigation) {
    return render(
      <DialogProvider>
        <SessionView
          investigation={inv}
          items={[]}
          events={[]}
          status="idle"
          streamErr={null}
          capabilities={null}
        />
      </DialogProvider>,
    );
  }

  it("offers retry when an archived, local-only session has a stored pushError", () => {
    // Mirrors the orphan-recovery state: archived, never pushed,
    // pushInProgress already cleared by Restore, pushError carries
    // the "push interrupted by server restart" string.
    renderArchived(
      makeInvestigation({
        archived: true,
        syncState: { status: "local-only" },
        pushError: "push interrupted by server restart",
      }),
    );
    expect(
      screen.getByRole("button", { name: /retry push to upstream/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/push interrupted by server restart/i),
    ).toBeInTheDocument();
  });

  it("offers initial push when an archived, local-only session has no pushError", () => {
    renderArchived(
      makeInvestigation({
        archived: true,
        syncState: { status: "local-only" },
      }),
    );
    expect(
      screen.getByRole("button", { name: /^push to upstream$/i }),
    ).toBeInTheDocument();
  });
});
