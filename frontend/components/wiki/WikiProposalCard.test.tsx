/* @vitest-environment jsdom */
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { WikiProposalCard, type WikiProposalPayload } from "@/components/wiki/WikiProposalCard";
import { api, ApiError, type Capabilities } from "@/lib/api";

const payload: WikiProposalPayload = {
  kind: "wiki_proposal_draft",
  proposal_id: "prop-aaaaaaaaaaaa",
  slug: "test-incident",
  new_md: "---\ntitle: Test\n---\n\nbody.",
  is_new: true,
};

const okCaps: Capabilities = {
  wiki: { valid: true },
} as unknown as Capabilities;

beforeEach(() => {
  vi.restoreAllMocks();
  // Mount path resolves the persisted resolution ledger first. Default
  // to "pending" so the action buttons render.
  vi.spyOn(api, "getWikiProposal").mockResolvedValue({
    status: "pending",
  } as never);
});

describe("WikiProposalCard streaming behaviour", () => {
  // The streaming gate on the wiki card used to disable both approve
  // and decline mid-stream, citing 409s from SendFollowUp. In practice
  // only decline-with-refinement-notes goes through that path; approve
  // hits the vault directly and decline-without-notes only writes a
  // resolution sidecar. Both are safe mid-stream, so the gate is just
  // friction.
  it("approve is enabled while the agent is streaming", async () => {
    render(
      <WikiProposalCard
        payload={payload}
        capabilities={okCaps}
        onSendRefinement={vi.fn()}
        streaming
      />,
    );
    const approve = await screen.findByRole("button", { name: /^approve$/i });
    expect(approve).toBeEnabled();
  });

  it("decline (no notes) is enabled while the agent is streaming", async () => {
    render(
      <WikiProposalCard
        payload={payload}
        capabilities={okCaps}
        onSendRefinement={vi.fn()}
        streaming
      />,
    );
    const decline = await screen.findByRole("button", { name: /^decline$/i });
    expect(decline).toBeEnabled();
  });

  it("decline with notes mid-stream: 409 on sendRefinement keeps card pending, preserves notes, shows error", async () => {
    const sendRefinement = vi
      .fn()
      .mockRejectedValue(new ApiError(409, "claude already streaming"));
    const declineSpy = vi.spyOn(api, "declineWikiProposal").mockResolvedValue();
    render(
      <WikiProposalCard
        payload={payload}
        capabilities={okCaps}
        onSendRefinement={sendRefinement}
        streaming
      />,
    );

    const textarea = await screen.findByPlaceholderText(/optional: ask the agent/i);
    await userEvent.type(textarea, "tighten lessons section");
    const declineBtn = screen.getByRole("button", { name: /decline \+ send notes/i });
    await userEvent.click(declineBtn);

    // sendRefinement was attempted (and 409'd); declineWikiProposal
    // must NOT have run — otherwise notes are lost and the proposal
    // is gone with no breadcrumb.
    await waitFor(() => expect(sendRefinement).toHaveBeenCalledTimes(1));
    expect(declineSpy).not.toHaveBeenCalled();

    // Card stays in pending state with notes still in the textarea
    // and an explanatory error shown inline.
    expect(screen.getByRole("button", { name: /decline \+ send notes/i })).toBeInTheDocument();
    expect(
      (screen.getByPlaceholderText(/optional: ask the agent/i) as HTMLTextAreaElement).value,
    ).toBe("tighten lessons section");
    expect(screen.getByText(/mid-turn|wait for the agent|notes weren't sent/i)).toBeInTheDocument();
  });

  it("decline with notes happy path: sends notes, then declines", async () => {
    const sendRefinement = vi.fn().mockResolvedValue(undefined);
    const declineSpy = vi.spyOn(api, "declineWikiProposal").mockResolvedValue();
    render(
      <WikiProposalCard
        payload={payload}
        capabilities={okCaps}
        onSendRefinement={sendRefinement}
      />,
    );
    const textarea = await screen.findByPlaceholderText(/optional: ask the agent/i);
    await userEvent.type(textarea, "tighten lessons section");
    await userEvent.click(
      screen.getByRole("button", { name: /decline \+ send notes/i }),
    );

    await waitFor(() => expect(sendRefinement).toHaveBeenCalledWith("tighten lessons section"));
    // Decline endpoint also receives the note so it lands in the
    // .resolved marker for future dispatched sub-agents to read via
    // list_proposals — independent of the chat-side send above.
    await waitFor(() =>
      expect(declineSpy).toHaveBeenCalledWith(
        payload.proposal_id,
        "tighten lessons section",
      ),
    );
    // Card transitions to declined.
    await screen.findByText(/^declined\.?/i);
  });
});
