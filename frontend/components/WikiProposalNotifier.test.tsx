import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render } from "@testing-library/react";
import type { StreamEnvelope } from "@/lib/api";
import type { StreamFilter } from "@/lib/stream";

type Handler = (env: StreamEnvelope) => void;

// Capture subscribed handlers so the test can synthesize stream envelopes.
const subscribers: { filter: StreamFilter; handler: Handler }[] = [];
vi.mock("@/lib/stream", () => ({
  useStream: () => ({
    subscribe: (filter: StreamFilter, handler: Handler) => {
      const entry = { filter, handler };
      subscribers.push(entry);
      return () => {
        const idx = subscribers.indexOf(entry);
        if (idx >= 0) subscribers.splice(idx, 1);
      };
    },
  }),
}));

function emit(env: Partial<StreamEnvelope> & { kind: string }) {
  for (const s of subscribers) s.handler(env as StreamEnvelope);
}

import { WikiProposalNotifier } from "./WikiProposalNotifier";

beforeEach(() => {
  subscribers.length = 0;
});

describe("WikiProposalNotifier", () => {
  it("dispatches c1:wiki-proposals-changed on a wiki_proposal_created envelope", () => {
    render(<WikiProposalNotifier>child</WikiProposalNotifier>);
    const onChange = vi.fn();
    window.addEventListener("c1:wiki-proposals-changed", onChange);

    emit({ kind: "wiki_proposal_created", wikiProposalCreated: { proposalID: "prop-1" } });
    expect(onChange).toHaveBeenCalledTimes(1);

    window.removeEventListener("c1:wiki-proposals-changed", onChange);
  });

  it("ignores unrelated global envelopes", () => {
    render(<WikiProposalNotifier>child</WikiProposalNotifier>);
    const onChange = vi.fn();
    window.addEventListener("c1:wiki-proposals-changed", onChange);

    emit({ kind: "watch_status" });
    expect(onChange).not.toHaveBeenCalled();

    window.removeEventListener("c1:wiki-proposals-changed", onChange);
  });

  it("subscribes on the global scope", () => {
    render(<WikiProposalNotifier>child</WikiProposalNotifier>);
    expect(subscribers).toHaveLength(1);
    expect(subscribers[0].filter).toEqual({ scope: "global" });
  });
});
