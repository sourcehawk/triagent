import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { ActivityPanel } from "./ActivityPanel";
import type { Investigation } from "@/lib/api";
import type { TranscriptItem } from "@/lib/events";

function makeInvestigation(overrides: Partial<Investigation> = {}): Investigation {
  return {
    id: "inv-test",
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

describe("ActivityPanel auto-operator group", () => {
  it("renders an Auto-operator group when operator tool_call envelopes are present", () => {
    const items: TranscriptItem[] = [
      {
        kind: "tool_call",
        id: "t1",
        toolId: "tu_1",
        name: "mcp__triagent-agent-operator__send_message",
        input: { text: "wiki" },
        startedAt: "2026-05-11T00:00:01Z",
      },
    ];
    render(
      <ActivityPanel
        investigation={makeInvestigation()}
        items={items}
      />,
    );
    expect(screen.getByText(/Auto-operator/i)).toBeInTheDocument();
  });

  it("does not render the Auto-operator group when no operator tool calls are present", () => {
    const items: TranscriptItem[] = [
      {
        kind: "tool_call",
        id: "t1",
        toolId: "tu_1",
        name: "mcp__triagent-k8s__get_pods",
        input: { ns: "default" },
        startedAt: "2026-05-11T00:00:01Z",
      },
    ];
    render(
      <ActivityPanel
        investigation={makeInvestigation()}
        items={items}
      />,
    );
    expect(screen.queryByText(/Auto-operator/i)).toBeNull();
  });
});
