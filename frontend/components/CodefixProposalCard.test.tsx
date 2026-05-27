import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { CodefixProposalCard } from "./CodefixProposalCard";
import { CodefixPRStateStoreCtx } from "./CodefixPRStateProvider";
import { api } from "@/lib/api";

const fixture = {
  proposal_id: "prop-test-aaaaaaaa",
  repo: "example-org/example-service",
  issue_url: "https://github.com/example-org/example-service/issues/42",
  issue_number: 42,
  pr_url: "https://github.com/example-org/example-service/pull/77",
  pr_number: 77,
  branch_name: "triagent-proposal/42-aaaaaaaa",
  summary: "Tightened the timeout in foo.go.",
};

describe("CodefixProposalCard", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("renders draft state with discard button + PR link", async () => {
    vi.spyOn(api, "getCodefixProposal").mockResolvedValue({
      ...fixture,
      pr_state: "draft",
    });
    render(<CodefixProposalCard payload={fixture} />);
    await screen.findByText(/Draft PR opened/i);
    expect(screen.getByText(fixture.summary)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /review on github/i }))
      .toHaveAttribute("href", fixture.pr_url);
    expect(screen.getByRole("link", { name: /example-org\/example-service#42/i }))
      .toHaveAttribute("href", fixture.issue_url);
    expect(screen.getByRole("button", { name: /discard/i })).toBeEnabled();
  });

  it("hides discard when state is merged", async () => {
    vi.spyOn(api, "getCodefixProposal").mockResolvedValue({
      ...fixture,
      pr_state: "merged",
    });
    render(<CodefixProposalCard payload={fixture} />);
    await screen.findByText(/Merged/i);
    expect(screen.queryByRole("button", { name: /discard/i })).not.toBeInTheDocument();
  });

  it("hides discard when state is closed", async () => {
    vi.spyOn(api, "getCodefixProposal").mockResolvedValue({
      ...fixture,
      pr_state: "closed",
    });
    render(<CodefixProposalCard payload={fixture} />);
    await screen.findByText(/Closed without merging/i);
    expect(screen.queryByRole("button", { name: /discard/i })).not.toBeInTheDocument();
  });

  it("shows error state when hydration fails", async () => {
    vi.spyOn(api, "getCodefixProposal").mockRejectedValue(new Error("boom"));
    render(<CodefixProposalCard payload={fixture} />);
    await screen.findByText(/PR state unknown/i);
    expect(screen.queryByRole("button", { name: /discard/i })).not.toBeInTheDocument();
  });

  it("prefers SSE-driven state over locally-fetched state", async () => {
    vi.spyOn(api, "getCodefixProposal").mockResolvedValue({
      ...fixture,
      pr_state: "draft",
    });
    // Provide a store that returns "merged" for our PR URL — simulating a
    // codefix_pr_state SSE envelope arriving before the local fetch settles.
    const store = {
      get: () => ({ state: "merged" }),
      upsert: () => {},
      subscribe: () => () => {},
    };
    render(
      <CodefixPRStateStoreCtx.Provider value={store}>
        <CodefixProposalCard payload={fixture} />
      </CodefixPRStateStoreCtx.Provider>,
    );
    await screen.findByText(/Merged/i);
    // Discard button must not appear for merged PRs.
    expect(screen.queryByRole("button", { name: /discard/i })).not.toBeInTheDocument();
  });

  it("posts to discard endpoint on click and disables optimistically", async () => {
    vi.spyOn(api, "getCodefixProposal").mockResolvedValue({
      ...fixture,
      pr_state: "draft",
    });
    const spy = vi.spyOn(api, "discardCodefixProposal").mockResolvedValue();
    render(<CodefixProposalCard payload={fixture} />);
    const btn = await screen.findByRole("button", { name: /discard/i });
    await userEvent.click(btn);
    await waitFor(() => expect(spy).toHaveBeenCalledWith(fixture.proposal_id));
    // After successful discard, state flips to "closed" and button vanishes.
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: /discard/i })).not.toBeInTheDocument(),
    );
  });
});
