"use client";

import { useEffect, type ReactNode } from "react";
import { useStream } from "@/lib/stream";

// WikiProposalNotifier bridges the launcher's wiki_proposal_created global
// event to the c1:wiki-proposals-changed DOM event the sidebar's pending list
// already listens for. A proposal drafted inside a playbook sub-agent surfaces
// live this way, independent of whether its (nested) transcript card rendered.
export function WikiProposalNotifier({ children }: { children: ReactNode }) {
  const stream = useStream();
  useEffect(() => {
    return stream.subscribe({ scope: "global" }, (env) => {
      if (env.kind !== "wiki_proposal_created") return;
      window.dispatchEvent(new CustomEvent("c1:wiki-proposals-changed"));
    });
  }, [stream]);
  return <>{children}</>;
}
