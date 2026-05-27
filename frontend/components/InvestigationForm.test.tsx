import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { InvestigationForm } from "./InvestigationForm";
import { api } from "@/lib/api";

beforeEach(() => {
  vi.restoreAllMocks();
});

describe("InvestigationForm", () => {
  it("renders inputs from schema fetched on mount", async () => {
    vi.spyOn(api, "getProfileInputs").mockResolvedValue([
      { id: "cluster_id", label: "Cluster", type: "cluster_id", optional: true, placeholder: "abc" },
      { id: "notes", label: "Notes", type: "textarea", optional: true, placeholder: "p" },
    ]);
    vi.spyOn(api, "getConnections").mockResolvedValue({ slack: false, incidentio: false, slack_channel_prefix: "" });

    render(<InvestigationForm onSubmit={() => {}} />);
    await screen.findByText("Cluster");
    await screen.findByPlaceholderText("p");
  });

  it("submits inputs as a map keyed by input id", async () => {
    vi.spyOn(api, "getProfileInputs").mockResolvedValue([
      { id: "notes", label: "Notes", type: "textarea", optional: true },
    ]);
    vi.spyOn(api, "getConnections").mockResolvedValue({ slack: false, incidentio: false, slack_channel_prefix: "" });

    const onSubmit = vi.fn();
    render(<InvestigationForm onSubmit={onSubmit} />);
    // Wait for schema to load, then find the Notes textarea by its label text.
    await screen.findByText("Notes");
    const textarea = screen.getByRole("textbox", { name: /notes/i });
    fireEvent.change(textarea, { target: { value: "hello" } });

    // Find and click the submit button.
    fireEvent.click(screen.getByRole("button", { name: /run preflight/i }));

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledWith(
        expect.objectContaining({
          inputs: { notes: { value: "hello" } },
        }),
      );
    });
  });

  it("shows a spinner before the schema loads", () => {
    vi.spyOn(api, "getProfileInputs").mockReturnValue(new Promise(() => { /* never resolves */ }));
    vi.spyOn(api, "getConnections").mockResolvedValue({ slack: false, incidentio: false, slack_channel_prefix: "" });
    const { container } = render(<InvestigationForm onSubmit={() => {}} />);
    // Spinner present, no fields yet.
    expect(container.querySelector('[role="status"]') ?? container.querySelector(".animate-spin")).toBeTruthy();
  });

  it("includes auto:true in submission when toggle is on", async () => {
    vi.spyOn(api, "getProfileInputs").mockResolvedValue([
      { id: "notes", label: "Notes", type: "textarea", optional: true, placeholder: "enter notes" },
    ]);
    vi.spyOn(api, "getConnections").mockResolvedValue({ slack: false, incidentio: false, slack_channel_prefix: "" });

    const onSubmit = vi.fn();
    render(<InvestigationForm onSubmit={onSubmit} />);
    await screen.findByPlaceholderText("enter notes");

    fireEvent.click(screen.getByLabelText(/Run in auto mode/i));
    fireEvent.click(screen.getByRole("button", { name: /run preflight/i }));

    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({ auto: true }),
    );
  });

  it("submits auto:false when the toggle is left off", async () => {
    vi.spyOn(api, "getProfileInputs").mockResolvedValue([
      { id: "notes", label: "Notes", type: "textarea", optional: true, placeholder: "enter notes" },
    ]);
    vi.spyOn(api, "getConnections").mockResolvedValue({ slack: false, incidentio: false, slack_channel_prefix: "" });

    const onSubmit = vi.fn();
    render(<InvestigationForm onSubmit={onSubmit} />);
    await screen.findByPlaceholderText("enter notes");

    fireEvent.click(screen.getByRole("button", { name: /run preflight/i }));

    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({ auto: false }),
    );
  });
});
