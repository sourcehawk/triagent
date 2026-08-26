import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { InvestigationForm } from "@/components/investigations/InvestigationForm";
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

  it("does not clobber typed prom override values when defaults resolve late", async () => {
    vi.spyOn(api, "getProfileInputs").mockResolvedValue([
      { id: "notes", label: "Notes", type: "textarea", optional: true },
    ]);
    vi.spyOn(api, "getConnections").mockResolvedValue({ slack: false, incidentio: false, slack_channel_prefix: "" });
    // Hold the prom-defaults promise open so we can simulate the
    // operator typing in the window between schema load and defaults
    // arrival.
    let resolveDefaults!: (v: { service: string; namespace: string; port: number } | null) => void;
    vi.spyOn(api, "getProfilePromDefaults").mockReturnValue(
      new Promise((res) => {
        resolveDefaults = res;
      }),
    );

    render(<InvestigationForm onSubmit={() => {}} />);
    await screen.findByText("Notes"); // schema loaded → form rendered

    // Open the prom overrides and type a custom service.
    fireEvent.click(screen.getByText("Prometheus configuration"));
    const serviceInput = await screen.findByPlaceholderText("default-prometheus");
    fireEvent.change(serviceInput, { target: { value: "my-custom-prom" } });

    // Defaults arrive AFTER the operator already typed. The functional
    // setter must preserve the operator's value, not overwrite it.
    // The operator left namespace blank, so the namespace default
    // should land — we wait on that as a "defaults have processed"
    // signal before asserting service was preserved.
    resolveDefaults({ service: "thanos-query", namespace: "thanos", port: 9090 });

    const namespaceInput = screen.getByPlaceholderText("prometheus") as HTMLInputElement;
    await waitFor(() => {
      expect(namespaceInput.value).toBe("thanos");
    });
    expect((serviceInput as HTMLInputElement).value).toBe("my-custom-prom");
  });

  it("fills empty prom override fields from defaults when nothing was typed", async () => {
    vi.spyOn(api, "getProfileInputs").mockResolvedValue([
      { id: "notes", label: "Notes", type: "textarea", optional: true },
    ]);
    vi.spyOn(api, "getConnections").mockResolvedValue({ slack: false, incidentio: false, slack_channel_prefix: "" });
    vi.spyOn(api, "getProfilePromDefaults").mockResolvedValue({
      service: "thanos-query",
      namespace: "thanos",
      port: 9090,
    });

    render(<InvestigationForm onSubmit={() => {}} />);
    await screen.findByText("Notes");
    fireEvent.click(screen.getByText("Prometheus configuration"));
    await waitFor(() => {
      const input = screen.getByPlaceholderText("default-prometheus") as HTMLInputElement;
      expect(input.value).toBe("thanos-query");
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

  describe("playbook selection", () => {
    const synced = { status: "synced", reason: "" } as const;
    function setup() {
      vi.spyOn(api, "getProfileInputs").mockResolvedValue([
        { id: "notes", label: "Notes", type: "textarea", optional: true, placeholder: "enter notes" },
      ]);
      vi.spyOn(api, "getConnections").mockResolvedValue({ slack: false, incidentio: false, slack_channel_prefix: "" });
      vi.spyOn(api, "listPlaybooks").mockResolvedValue([
        { id: "investigation", source: "system", locked: true, nodeCount: 1, yaml: "", syncState: synced, type: "general" },
        { id: "release_verification", symptom: "Verify a release", source: "user", nodeCount: 1, yaml: "", syncState: synced, type: "general" },
        { id: "pod_crashloop", symptom: "Pods restart in a loop", source: "plugin", nodeCount: 1, yaml: "", syncState: synced, type: "investigation" },
      ]);
      vi.spyOn(api, "listPlaybookTypes").mockResolvedValue([
        { name: "investigation", description: "Hunt a live incident", source: "system", tracked: true },
        { name: "general", description: "Routine operational work", source: "system", tracked: true },
      ]);
    }

    it("submits without a playbook by default", async () => {
      setup();
      const onSubmit = vi.fn();
      render(<InvestigationForm onSubmit={onSubmit} />);
      await screen.findByPlaceholderText("enter notes");
      await screen.findByRole("option", { name: /release_verification/ });

      fireEvent.click(screen.getByRole("button", { name: /run preflight/i }));
      expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ playbook: undefined }));
    });

    it("submits the chosen playbook id and hides locked entries", async () => {
      setup();
      const onSubmit = vi.fn();
      render(<InvestigationForm onSubmit={onSubmit} />);
      await screen.findByPlaceholderText("enter notes");
      await screen.findByRole("option", { name: /release_verification/ });
      expect(within(screen.getByLabelText(/^Playbook/i)).queryByRole("option", { name: /^investigation/ })).toBeNull();

      fireEvent.change(screen.getByLabelText(/^Playbook/i), { target: { value: "release_verification" } });
      fireEvent.click(screen.getByRole("button", { name: /run preflight/i }));
      expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ playbook: "release_verification" }));
    });

    it("filters playbooks by category and describes both picks", async () => {
      setup();
      render(<InvestigationForm onSubmit={() => {}} />);
      await screen.findByRole("option", { name: /release_verification/ });

      fireEvent.change(screen.getByLabelText(/Category/i), { target: { value: "general" } });
      expect(screen.getByText("Routine operational work")).toBeInTheDocument();
      expect(screen.queryByRole("option", { name: /pod_crashloop/ })).toBeNull();
      expect(screen.getByRole("option", { name: /release_verification/ })).toBeInTheDocument();

      fireEvent.change(screen.getByLabelText(/^Playbook/i), { target: { value: "release_verification" } });
      expect(screen.getByText("Verify a release")).toBeInTheDocument();
    });

    it("clears a chosen playbook when the category no longer contains it", async () => {
      setup();
      const onSubmit = vi.fn();
      render(<InvestigationForm onSubmit={onSubmit} />);
      await screen.findByRole("option", { name: /release_verification/ });

      fireEvent.change(screen.getByLabelText(/^Playbook/i), { target: { value: "release_verification" } });
      // Picking a playbook snaps the category to its type.
      expect((screen.getByLabelText(/Category/i) as HTMLSelectElement).value).toBe("general");

      fireEvent.change(screen.getByLabelText(/Category/i), { target: { value: "investigation" } });
      fireEvent.click(screen.getByRole("button", { name: /run preflight/i }));
      expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ playbook: undefined }));
    });
  });
});
