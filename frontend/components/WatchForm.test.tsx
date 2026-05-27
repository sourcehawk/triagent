import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { WatchForm } from "./WatchForm";

describe("WatchForm", () => {
  it("renders github fields when kind is github", () => {
    render(<WatchForm onSubmit={vi.fn()} />);
    expect(screen.getByLabelText(/owner/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/repo/i)).toBeInTheDocument();
  });
  it("switches to slack fields when kind=slack_channel selected", () => {
    render(<WatchForm onSubmit={vi.fn()} />);
    fireEvent.click(screen.getByLabelText(/slack channel/i));
    expect(screen.getByLabelText(/channel id/i)).toBeInTheDocument();
  });
  it("posts form on submit", () => {
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
  });
});
