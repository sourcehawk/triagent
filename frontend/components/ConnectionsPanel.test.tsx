import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { ConnectionsPanel } from "./ConnectionsPanel";
import { api, type ConnectionStatus } from "@/lib/api";
import { DialogProvider } from "@/lib/dialog";

// The cloud pills live in the manage-connections modal alongside the Slack and
// incident.io cards; open it before asserting on cloud content.
async function renderPanelAndOpenModal() {
  render(
    <DialogProvider>
      <ConnectionsPanel />
    </DialogProvider>,
  );
  await waitFor(() => expect(api.getConnections).toHaveBeenCalled());
  await userEvent.click(
    screen.getByRole("button", { name: "manage connections" }),
  );
}

const baseStatus: ConnectionStatus = {
  slack: false,
  incidentio: false,
  slack_channel_prefix: "",
};

describe("ConnectionsPanel cloud pills", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("renders a read-only cloud pill per entry with the assumed identity", async () => {
    vi.spyOn(api, "getConnections").mockResolvedValue({
      ...baseStatus,
      cloud: [
        {
          provider: "gcp",
          assumed_identity: "triage-ro@prod.iam.gserviceaccount.com",
          valid: true,
        },
        {
          provider: "aws",
          assumed_identity: "arn:aws:iam::1:role/triage-ro",
          valid: false,
          hint: "run: aws sso login",
        },
      ],
    });

    await renderPanelAndOpenModal();

    expect(
      await screen.findByText("triage-ro@prod.iam.gserviceaccount.com"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("arn:aws:iam::1:role/triage-ro"),
    ).toBeInTheDocument();
  });

  it("shows the reauth hint only for an invalid source", async () => {
    vi.spyOn(api, "getConnections").mockResolvedValue({
      ...baseStatus,
      cloud: [
        {
          provider: "aws",
          assumed_identity: "arn:aws:iam::1:role/triage-ro",
          valid: false,
          hint: "run: aws sso login",
        },
      ],
    });

    await renderPanelAndOpenModal();

    expect(await screen.findByText("run: aws sso login")).toBeInTheDocument();
  });

  it("renders no edit affordance for cloud pills", async () => {
    vi.spyOn(api, "getConnections").mockResolvedValue({
      ...baseStatus,
      cloud: [
        {
          provider: "gcp",
          assumed_identity: "triage-ro@prod.iam.gserviceaccount.com",
          valid: true,
        },
      ],
    });

    await renderPanelAndOpenModal();

    await screen.findByText("triage-ro@prod.iam.gserviceaccount.com");
    const pill = screen
      .getByText("triage-ro@prod.iam.gserviceaccount.com")
      .closest("[data-cloud-pill]");
    expect(pill).not.toBeNull();
    expect(pill!.querySelector("button")).toBeNull();
    expect(pill!.querySelector("input")).toBeNull();
  });

  it("renders no cloud section when there are no cloud sources", async () => {
    vi.spyOn(api, "getConnections").mockResolvedValue(baseStatus);

    await renderPanelAndOpenModal();

    await waitFor(() => {
      expect(api.getConnections).toHaveBeenCalled();
    });
    expect(screen.queryByTestId("cloud-connections")).toBeNull();
  });
});
