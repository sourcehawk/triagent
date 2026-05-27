import type { StreamEnvelope } from "./api";

// StreamFilter selects envelopes from the multiplex stream by scope.
// Investigation and editor scopes match by id; global matches
// envelopes with neither scope id set (launcher-wide events);
// anyInvestigation matches every investigation-scoped envelope
// regardless of id — used by the sidebar to keep streaming/idle
// status fresh for every row without one subscription per item.
export type StreamFilter =
  | { scope: "global" }
  | { scope: "investigation"; id: string }
  | { scope: "editorSession"; id: string }
  | { scope: "anyInvestigation" };

export function matchesFilter(env: StreamEnvelope, filter: StreamFilter): boolean {
  switch (filter.scope) {
    case "global":
      return !env.investigationId && !env.editorSessionId;
    case "investigation":
      return env.investigationId === filter.id;
    case "editorSession":
      return env.editorSessionId === filter.id;
    case "anyInvestigation":
      return !!env.investigationId;
  }
}
