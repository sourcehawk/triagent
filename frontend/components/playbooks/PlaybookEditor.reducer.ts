import { type ProposalDraftPayload } from "@/components/playbooks/ProposalCard";
import {
  type Playbook,
  type PlaybookCommit,
  type PlaybookListItem,
  type PlaybookNode,
} from "@/lib/playbook";

// Parser for the playbook drawer. Validates the two ids the editor's
// UI relies on (the card fetches the diff bodies itself), dedupes tabs
// by playbook_id (latest draft for each id wins).
export function parsePlaybookProposal(
  raw: unknown,
): { key: string; payload: ProposalDraftPayload } | null {
  const r = raw as Partial<ProposalDraftPayload>;
  if (
    typeof r?.proposal_id !== "string" ||
    typeof r?.playbook_id !== "string"
  ) {
    return null;
  }
  return { key: r.playbook_id, payload: r as ProposalDraftPayload };
}

// isStubbedDraft returns true when the draft has not been substantively
// edited from the empty template — i.e., the operator typed at most an
// id (which we strip from the comparison). The `active` field is also
// ignored because it's stamped at write time, not authored by the
// operator. Used by the __new approval flow to decide whether to
// navigate to a freshly-saved sibling proposal: if the draft is still
// the stub, navigation isn't disruptive (no operator work to lose).
export function isStubbedDraft(draft: Playbook, original: Playbook): boolean {
  const stripIDActive = (p: Playbook) => {
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const { id, active, ...rest } = p;
    return rest;
  };
  return JSON.stringify(stripIDActive(draft)) === JSON.stringify(stripIDActive(original));
}

export type LoadState =
  | { kind: "loading" }
  | {
      kind: "loaded";
      original: Playbook;
      source: SourceTag;
      // True when the playbook ships bundled with the launcher
      // (system tier). Disables save / delete / type-change /
      // push-PR — locked entries are owned by the launcher binary,
      // not the operator.
      locked: boolean;
      isNew: boolean;
      disabled: boolean;
      // Commit history for the commits dropdown (up to 50, merged
      // across upstream + local repos). Populated from the detail
      // response; empty for brand-new (unsaved) playbooks.
      commits: PlaybookCommit[];
      // The sha the operator is currently viewing. Undefined means
      // HEAD (latest). Resets to undefined after a save.
      viewedCommit?: string;
      // Raw upstream-clone YAML when this id has an upstream
      // counterpart. Empty for user-only playbooks. The push-PR gate
      // compares the operator's draft against this — a draft that
      // matches upstream produces a no-op PR, so the button locks.
      upstreamYAML?: string;
    }
  | { kind: "error"; message: string };

export type SourceTag = "plugin" | "system" | "user" | "override" | "broken";

// Reducer-style updates against the editable Playbook. Each field-level
// edit dispatches a partial that's merged in. Node operations (rename,
// delete, add) take dedicated actions because they touch multiple fields.
export type Action =
  | { type: "set"; next: Playbook }
  | {
      type: "setMeta";
      meta: Partial<
        Pick<
          Playbook,
          | "id"
          | "symptom"
          | "description"
          | "entrypoint"
          | "type"
          | "active"
          | "services"
          | "errors"
          | "symptoms"
        >
      >;
    }
  | { type: "setNode"; nodeId: string; node: PlaybookNode }
  | { type: "renameNode"; oldId: string; newId: string }
  | { type: "addNode"; nodeId: string }
  | { type: "deleteNode"; nodeId: string };

// pickActiveCommitSha returns the sha of the commit that wrote the
// currently-active YAML for a playbook, derived from the merged
// commits list + the source slot. Newest-first ordering means the
// first source-matching commit is the active one. Returns undefined
// when there's no candidate (e.g. a system playbook that lives only
// in the launcher binary, or a fresh playbook with no history).
export function pickActiveCommitSha(
  commits: PlaybookCommit[],
  source: PlaybookListItem["source"],
): string | undefined {
  // user / override: latest commit on the local user-playbooks repo
  // is what's on disk and active.
  // plugin: latest commit on the upstream-playbooks repo wins.
  // system / broken / disabled: no commit history we can pin to.
  let want: PlaybookCommit["source"] | undefined;
  if (source === "user" || source === "override") want = "local";
  else if (source === "plugin") want = "upstream";
  if (!want) return undefined;
  const m = commits.find((c) => c.source === want);
  return m?.sha;
}

export type ViewMode =
  | { kind: "graph" }
  | { kind: "yaml" }
  | { kind: "proposal"; proposalId: string };

export function reduce(state: Playbook, action: Action): Playbook {
  switch (action.type) {
    case "set":
      return action.next;
    case "setMeta":
      return { ...state, ...action.meta };
    case "setNode":
      return {
        ...state,
        nodes: { ...state.nodes, [action.nodeId]: action.node },
      };
    case "renameNode": {
      if (action.oldId === action.newId || !action.newId) return state;
      if (state.nodes[action.newId]) return state; // collision; ignored
      const nextNodes: Record<string, PlaybookNode> = {};
      for (const [id, node] of Object.entries(state.nodes)) {
        nextNodes[id === action.oldId ? action.newId : id] = {
          ...node,
          next: node.next?.map((b) =>
            b.goto === action.oldId ? { ...b, goto: action.newId } : b,
          ),
        };
      }
      return {
        ...state,
        nodes: nextNodes,
        entrypoint:
          state.entrypoint === action.oldId ? action.newId : state.entrypoint,
      };
    }
    case "addNode": {
      if (state.nodes[action.nodeId]) return state;
      return {
        ...state,
        nodes: {
          ...state.nodes,
          [action.nodeId]: { description: "" },
        },
      };
    }
    case "deleteNode": {
      const nextNodes: Record<string, PlaybookNode> = { ...state.nodes };
      delete nextNodes[action.nodeId];
      return { ...state, nodes: nextNodes };
    }
  }
}
