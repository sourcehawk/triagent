import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { SignalCard } from "./SignalCard";
import type { SignalRecord } from "@/lib/api";

// SignalCard uses useRouter from next/navigation; stub the module so
// jsdom doesn't blow up trying to reach the App Router context.
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn() }),
}));

const base: SignalRecord = {
  id: "S1", watchID: "w1", createdAt: "2026-05-11T10:05:20Z",
  citedItemIDs: ["I1"], outcome: "investigation_started", investigationID: "INV1",
  clusters: ["prod"], briefing: "Engine OOMs after upgrade",
};

describe("SignalCard", () => {
  it("renders started outcome with investigation link", () => {
    render(<SignalCard signal={base} />);
    expect(screen.getByText(/investigation_started/i)).toBeInTheDocument();
    // Next's <Link> normalizes "/investigations/?id=…" to drop the trailing
    // slash before the query — the live investigation route is what we want.
    expect(screen.getByRole("link", { name: /investigation/i })).toHaveAttribute("href", "/investigations?id=INV1");
  });
  it("renders unclear with Start CTA", () => {
    const s: SignalRecord = { ...base, outcome: "unclear", investigationID: undefined, reason: "no signal" };
    render(<SignalCard signal={s} />);
    expect(screen.getByText(/start investigation/i)).toBeInTheDocument();
  });
  it("renders dismissed with wiki slug chips", () => {
    const s: SignalRecord = { ...base, outcome: "dismissed", investigationID: undefined, reason: "known noop", dismissedWikiSlugs: ["keda-cooldown"] };
    render(<SignalCard signal={s} />);
    expect(screen.getByText(/keda-cooldown/)).toBeInTheDocument();
  });
});
