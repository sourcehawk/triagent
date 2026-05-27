import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { WatchesList } from "./WatchesList";
import type { Watch } from "@/lib/api";

const w: Watch = {
  id: "w1", name: "c1 triage",
  source: { kind: "github_issues", owner: "example-org", repo: "example", labels: ["bug"] },
  polling: { intervalSeconds: 300 },
  autoStart: { enabled: true, maxConcurrent: 1 },
  createdAt: "2026-05-11T10:00:00Z", enabled: true,
};

describe("WatchesList", () => {
  it("renders empty-state message when no watches", () => {
    render(<WatchesList watches={[]} />);
    expect(screen.getByText(/No watches yet/i)).toBeInTheDocument();
  });
  it("renders watch name + source summary + auto-start chip", () => {
    render(<WatchesList watches={[w]} />);
    expect(screen.getByText("c1 triage")).toBeInTheDocument();
    expect(screen.getByText(/example-org\/example/)).toBeInTheDocument();
    expect(screen.getByText(/auto-start/i)).toBeInTheDocument();
  });
});
