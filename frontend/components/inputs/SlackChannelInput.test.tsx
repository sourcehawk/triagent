import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { SlackChannelInput } from "./SlackChannelInput";
import { api } from "@/lib/api";

beforeEach(() => {
  vi.restoreAllMocks();
});

const schema = {
  id: "slack_channel",
  label: "Slack channel",
  type: "slack_channel" as const,
  optional: true,
  placeholder: "https://x.slack.com/archives/C1",
};

const emptyValue = { id: "", name: "", url: "" };

describe("SlackChannelInput (slack not connected)", () => {
  it("renders the URL input fallback", async () => {
    vi.spyOn(api, "getConnections").mockResolvedValue({ slack: false, incidentio: false, slack_channel_prefix: "" });
    render(
      <SlackChannelInput
        schema={schema}
        value={emptyValue}
        onChange={vi.fn()}
      />,
    );
    const input = await screen.findByPlaceholderText("https://x.slack.com/archives/C1");
    expect(input).toBeInTheDocument();
    expect(screen.getByText(/Slack isn't connected/)).toBeInTheDocument();
  });

  it("fires onChange with url when URL field changes", async () => {
    vi.spyOn(api, "getConnections").mockResolvedValue({ slack: false, incidentio: false, slack_channel_prefix: "" });
    const onChange = vi.fn();
    render(
      <SlackChannelInput
        schema={schema}
        value={emptyValue}
        onChange={onChange}
      />,
    );
    const input = await screen.findByPlaceholderText("https://x.slack.com/archives/C1");
    fireEvent.change(input, { target: { value: "https://example.com" } });
    expect(onChange).toHaveBeenCalledWith({ id: "", name: "", url: "https://example.com" });
  });
});

describe("SlackChannelInput (slack connected)", () => {
  it("renders the SlackChannelPicker", async () => {
    vi.spyOn(api, "getConnections").mockResolvedValue({ slack: true, incidentio: false, slack_channel_prefix: "" });
    render(
      <SlackChannelInput
        schema={schema}
        value={emptyValue}
        onChange={vi.fn()}
      />,
    );
    // Wait for the picker to mount (after the connections fetch resolves).
    // Confirm that the URL fallback's "Slack isn't connected" message is NOT shown.
    await waitFor(() => {
      expect(screen.queryByText(/Slack isn't connected/)).toBeNull();
    });
  });
});
