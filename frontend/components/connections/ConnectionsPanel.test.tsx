import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { ConnectionsPanel } from "@/components/connections/ConnectionsPanel";
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

  it("renders the principal + reach shape per provider", async () => {
    vi.spyOn(api, "getConnections").mockResolvedValue({
      ...baseStatus,
      cloud: [
        {
          alias: "prod-gcp",
          provider: "gcp",
          assumed_identity: "triage-ro@prod.iam.gserviceaccount.com",
          projects: ["prod-platform", "prod-data"],
          valid: true,
        },
        {
          alias: "prod-aws",
          provider: "aws",
          accounts: ["111111111111", "222222222222"],
          source_profile: "sso-admin",
          valid: false,
          hint: "run: aws sso login",
        },
      ],
    });

    await renderPanelAndOpenModal();

    expect(await screen.findByText("prod-gcp")).toBeInTheDocument();
    expect(screen.getByText("prod-aws")).toBeInTheDocument();
    // gcp: the impersonated service account over its allowlisted project count.
    expect(
      screen.getByText("triage-ro@prod.iam.gserviceaccount.com"),
    ).toBeInTheDocument();
    const gcpReach = screen.getByText("2 projects");
    expect(gcpReach).toHaveAttribute("title", "prod-platform, prod-data");
    // aws: the SSO base profile over its account count, never a single identity.
    expect(screen.getByText("base: sso-admin")).toBeInTheDocument();
    const awsReach = screen.getByText("2 accounts");
    expect(awsReach).toHaveAttribute("title", "111111111111, 222222222222");
  });

  it("renders a one-entry reach as singular, and an empty gcp scope as all projects", async () => {
    vi.spyOn(api, "getConnections").mockResolvedValue({
      ...baseStatus,
      cloud: [
        {
          alias: "single-aws",
          provider: "aws",
          accounts: ["123456789012"],
          source_profile: "sso-admin",
          valid: true,
        },
        {
          alias: "open-gcp",
          provider: "gcp",
          assumed_identity: "ro@p.iam.gserviceaccount.com",
          valid: true,
        },
      ],
    });

    await renderPanelAndOpenModal();

    expect(await screen.findByText("1 account")).toBeInTheDocument();
    expect(screen.getByText("all projects")).toBeInTheDocument();
  });

  it("shows the reauth hint only for an invalid source", async () => {
    vi.spyOn(api, "getConnections").mockResolvedValue({
      ...baseStatus,
      cloud: [
        {
          alias: "prod-aws",
          provider: "aws",
          accounts: ["111111111111"],
          source_profile: "sso-admin",
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
          alias: "prod-gcp",
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
