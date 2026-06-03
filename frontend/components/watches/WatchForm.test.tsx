import { render, screen, fireEvent, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { api } from "@/lib/api";
import { WatchForm } from "@/components/watches/WatchForm";

describe("WatchForm", () => {
  beforeEach(() => {
    // The mount effect fetches connection status and setState's on it.
    // Stub it so the async update is deterministic, then flush it inside
    // act() (flushEffects) so React doesn't warn about an unwrapped update.
    vi.spyOn(api, "getConnections").mockResolvedValue({
      slack: false,
      incidentio: false,
      slack_channel_prefix: "",
    });
  });

  // flushEffects settles the mount effect's connection fetch inside act so
  // its trailing setConn doesn't land outside act().
  const flushEffects = () => act(async () => {});

  it("renders github fields when kind is github", async () => {
    render(<WatchForm onSubmit={vi.fn()} />);
    expect(screen.getByLabelText(/owner/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/repo/i)).toBeInTheDocument();
    await flushEffects();
  });
  it("switches to slack fields when kind=slack_channel selected", async () => {
    render(<WatchForm onSubmit={vi.fn()} />);
    fireEvent.click(screen.getByLabelText(/slack channel/i));
    expect(screen.getByLabelText(/channel id/i)).toBeInTheDocument();
    await flushEffects();
  });
  it("posts form on submit", async () => {
    const onSubmit = vi.fn();
    render(<WatchForm onSubmit={onSubmit} />);
    fireEvent.change(screen.getByLabelText(/name/i), { target: { value: "n" } });
    fireEvent.change(screen.getByLabelText(/owner/i), { target: { value: "o" } });
    fireEvent.change(screen.getByLabelText(/repo/i), { target: { value: "r" } });
    fireEvent.click(screen.getByText(/create watch/i));
    expect(onSubmit).toHaveBeenCalledOnce();
    const arg = onSubmit.mock.calls[0][0];
    expect(arg.name).toBe("n");
    expect(arg.source.kind).toBe("github_issues");
    await flushEffects();
  });
});
