/* @vitest-environment jsdom */
import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { ProposalCard, type ProposalDraftPayload } from "@/components/playbooks/ProposalCard";
import { parsePlaybookProposal } from "@/components/playbooks/PlaybookEditor.reducer";
import { api } from "@/lib/api";

// react-diff-viewer-continued is loaded via next/dynamic, which doesn't
// resolve under jsdom. Stub it with a marker so the tests can assert
// which sides the diff received without rendering the real viewer.
vi.mock("next/dynamic", () => ({
  default: () =>
    function DiffStub(props: { oldValue: string; newValue: string }) {
      return (
        <div data-testid="diff-stub">
          <span data-testid="diff-old">{props.oldValue}</span>
          <span data-testid="diff-new">{props.newValue}</span>
        </div>
      );
    },
}));

// The tool result carries only the ids; the bodies come from the
// proposal endpoint.
const payload: ProposalDraftPayload = {
  proposal_id: "prop-aaaaaaaaaaaa",
  playbook_id: "testpb",
  why: "because",
};

const newYaml =
  "id: testpb\nschema_version: 1\nsymptom: s\nentrypoint: a\nnodes:\n  a:\n    description: a\n    terminal_advice: done\n";
const baseYaml =
  "id: testpb\nschema_version: 1\nsymptom: old\nentrypoint: a\nnodes:\n  a:\n    description: a\n    terminal_advice: done\n";

beforeEach(() => {
  vi.restoreAllMocks();
});

describe("ProposalCard body hydration", () => {
  it("fetches base/new YAML from the proposal endpoint when the payload has none", async () => {
    vi.spyOn(api, "getPlaybookProposal").mockResolvedValue({
      proposal_id: payload.proposal_id,
      status: "pending",
      playbook_id: "testpb",
      base_yaml: baseYaml,
      new_yaml: newYaml,
    });
    render(<ProposalCard payload={payload} onSendRefinement={vi.fn()} defaultTab="yaml" />);
    expect(await screen.findByText("current → proposed")).toBeInTheDocument();
    expect((await screen.findByTestId("diff-new")).textContent).toBe(newYaml);
    expect(screen.getByTestId("diff-old").textContent).toBe(baseYaml);
    expect(screen.getByRole("button", { name: /approve/i })).toBeInTheDocument();
  });

  it("treats an empty fetched base as a new playbook", async () => {
    vi.spyOn(api, "getPlaybookProposal").mockResolvedValue({
      proposal_id: payload.proposal_id,
      status: "pending",
      playbook_id: "testpb",
      new_yaml: newYaml,
    });
    render(<ProposalCard payload={payload} onSendRefinement={vi.fn()} defaultTab="yaml" />);
    expect(await screen.findByText("new playbook")).toBeInTheDocument();
  });

  it("prefers bodies already present on the payload", async () => {
    const spy = vi.spyOn(api, "getPlaybookProposal").mockResolvedValue({
      proposal_id: payload.proposal_id,
      status: "pending",
    });
    render(
      <ProposalCard
        payload={{ ...payload, base_yaml: baseYaml, new_yaml: newYaml }}
        onSendRefinement={vi.fn()}
        defaultTab="yaml"
      />,
    );
    expect((await screen.findByTestId("diff-new")).textContent).toBe(newYaml);
    expect(spy).toHaveBeenCalledOnce();
  });

  it("explains when a resolved proposal's body is no longer available", async () => {
    vi.spyOn(api, "getPlaybookProposal").mockResolvedValue({
      proposal_id: payload.proposal_id,
      status: "declined",
    });
    render(<ProposalCard payload={payload} onSendRefinement={vi.fn()} />);
    expect(await screen.findByText(/declined\./)).toBeInTheDocument();
    expect(screen.getByText(/no longer available/i)).toBeInTheDocument();
    expect(screen.queryByTestId("diff-stub")).not.toBeInTheDocument();
  });
});

describe("parsePlaybookProposal", () => {
  it("accepts a tool result that carries only the ids", () => {
    const out = parsePlaybookProposal({ proposal_id: "p1", playbook_id: "testpb" });
    expect(out?.key).toBe("testpb");
    expect(out?.payload.proposal_id).toBe("p1");
  });
});
