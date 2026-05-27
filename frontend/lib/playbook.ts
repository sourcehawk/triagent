// TypeScript projection of the Go schema in
// mcp/internal/strategies/playbook.go. Kept narrow on purpose — the YAML
// preserves any fields we don't model here, and js-yaml round-trips them
// transparently. The validate() helper mirrors the structural checks that
// `triagent-mcp validate-playbook` runs server-side, so the editor surfaces
// dangling-goto / missing-entrypoint errors without a round trip.

import yaml from "js-yaml";

import type { SyncState } from "./sync-state";

export type SuggestedCall = {
  tool: string;
  args?: Record<string, unknown>;
};

export type Branch = {
  condition: string;
  goto: string;
};

// PlaybookType is the canonical classifier used by the agent's
// list_playbooks filter. Stays loose (string) at the type level so a
// future type can be introduced via YAML before the constants list
// catches up.
export type PlaybookType = "investigation" | "general" | (string & {});

export const DEFAULT_PLAYBOOK_TYPE: PlaybookType = "investigation";

export type PlaybookNode = {
  description: string;
  suggested_calls?: SuggestedCall[];
  expected_findings?: string[];
  next?: Branch[];
  terminal_advice?: string;
  // Terminal-only: ids of other playbooks the operator/agent should
  // continue with after this leaf. Matches the `handoff` YAML field;
  // the editor renders them as clickable links and the validator (both
  // here and server-side) flags shape-invalid entries. Cross-playbook
  // id existence is best-effort: the editor compares against the
  // loaded playbook list, but a single-document validate can't see
  // other playbooks so a typoed id won't fail save server-side.
  handoff?: string[];
  // Sub-flow only: id of another playbook to walk to its non-handoff
  // terminal, then resume this node's `next`. Mutually exclusive with
  // terminal_advice / handoff / suggested_calls; requires `next`. Same
  // soft cross-playbook resolution as handoff (typo surfaces at
  // runtime, not in single-doc validate).
  delegate_to?: string;
};

export type Playbook = {
  id: string;
  symptom: string;
  description?: string;
  entrypoint: string;
  nodes: Record<string, PlaybookNode>;
  // Whether the loader picks this file. Unset = treated as true so
  // freshly-authored playbooks load. Operators flip it via the editor's
  // enable/disable toggle.
  active?: boolean;
  // Coarse classifier — see PlaybookType. Optional; falls back to
  // DEFAULT_PLAYBOOK_TYPE ("investigation") when unset.
  type?: PlaybookType;
  // Entity-correlation arrays — fed into playbook_correlate scoring.
  // Mirrors the backend struct fields added in Task 2 (yaml tags with
  // omitempty, so absent arrays round-trip as absent, not as []).
  services?: string[];
  errors?: string[];
  symptoms?: string[];
};

export type PlaybookCommit = {
  sha: string;
  summary: string;
  date: string;
  source: "local" | "upstream";
};

// PlaybookListItem matches the JSON `apiHandlers.handleListPlaybooks`
// returns: parsed metadata + raw YAML so the editor never needs a second
// fetch to populate the graph. Each user file is the single
// source-of-truth for its id; git history replaces the versioned-file
// layout. The detail endpoint (GET /api/playbooks/{id}) augments the
// response with a commits[] field.
//
//   - disabled: true when a tombstone (active=false) has suppressed the
//     embedded version (operator disabled an unwanted system playbook).
export type PlaybookListItem = {
  id: string;
  symptom?: string;
  description?: string;
  // Provenance only: where the YAML on disk came from. The "is it
  // currently loaded?" question is answered by the `disabled` flag +
  // the StatusPill, NOT by source. An override with disabled=true
  // shows as source=override + status=disabled. "plugin" = upstream
  // library (overridable). "system" = launcher-bundled meta (locked).
  source: "plugin" | "system" | "user" | "override" | "broken";
  // True for entries the launcher ships bundled (system metas: master
  // investigation entrypoint + workflow metas). The editor disables
  // save / delete / type-change / push-PR when locked — these files
  // are owned by the launcher binary, not the operator.
  locked?: boolean;
  nodeCount: number;
  yaml: string;
  disabled?: boolean;
  type?: PlaybookType;
  // syncState is the authoritative drift answer from the launcher
  // (computed by playbookSyncState in internal/server/sync_state.go).
  // The list and the editor's push-PR gate both read this via the
  // sync-state endpoint, so they can't disagree about whether a file
  // is drifted.
  syncState: SyncState;
  // Raw upstream-clone YAML for this id when one exists. Used by the
  // editor for diff display only — the push-PR drift gate goes through
  // the sync-state endpoint (server-authoritative), not through this
  // field.
  upstreamYAML?: string;
  // The playbook contract version this YAML targets. Empty / 0 in
  // the YAML is normalised to 1 server-side. Phase 2 will surface
  // a "Migrate" affordance when this differs from the launcher's
  // current schema.
  schemaVersion?: number;
  // True when schemaVersion ≠ the launcher's current schema. The
  // playbook still loads (older schemas are accepted; the loader
  // rejects only future schemas), but the operator should migrate.
  schemaMismatch?: boolean;
  // Populated only by the detail endpoint (GET /api/playbooks/{id}).
  // The 50 most recent commits touching this playbook across both
  // the user and upstream repos, merged and sorted by date.
  commits?: PlaybookCommit[];
};

export type ToolEntry = {
  server: string;
  name: string;
  description: string;
  // Inputs the tool accepts, reflected from the Go server's
  // registered input struct. Used by the MCP catalog view (renders
  // each input + its description on expand) and by the playbook
  // editor's suggested-call picker (offers them as opt-in args).
  inputs?: ToolInput[];
};

export type ToolInput = {
  name: string;
  type: "string" | "integer" | "number" | "boolean" | "array" | "object" | string;
  description?: string;
  required?: boolean;
};

// parsePlaybookYAML loads raw YAML into a Playbook. Throws if the YAML
// is malformed; callers should treat it as a parse error and surface
// the message to the user.
export function parsePlaybookYAML(raw: string): Playbook {
  const parsed = yaml.load(raw) as Playbook | null;
  if (!parsed || typeof parsed !== "object") {
    throw new Error("playbook YAML is empty or not an object");
  }
  if (!parsed.nodes || typeof parsed.nodes !== "object") {
    parsed.nodes = {};
  }
  return parsed;
}

// dumpPlaybookYAML produces YAML the launcher's validator should accept.
// We use lineWidth: 100 to match the existing playbook formatting and
// noRefs to keep the output diff-friendly.
export function dumpPlaybookYAML(p: Playbook): string {
  return yaml.dump(p, {
    lineWidth: 100,
    noRefs: true,
    sortKeys: false,
    quotingType: '"',
  });
}

// validate runs the same structural checks as the Go-side validator
// (mcp/internal/strategies/playbook.go validateOne). Used for live
// feedback in the YamlPanel — the server still re-validates on save, so
// this is best-effort but consistent.
export function validate(p: Playbook): string[] {
  const errs: string[] = [];
  if (!p.id) errs.push("missing id");
  if (!p.symptom) errs.push("missing symptom");
  if (!p.entrypoint) {
    errs.push("missing entrypoint");
  } else if (!p.nodes[p.entrypoint]) {
    errs.push(`entrypoint "${p.entrypoint}" not found in nodes`);
  }
  if (Object.keys(p.nodes).length === 0) {
    errs.push("no nodes defined");
  }
  for (const [id, node] of Object.entries(p.nodes)) {
    if (!id) errs.push("empty node id");
    if (!node.description) errs.push(`node "${id}" has empty description`);
    for (const [i, br] of (node.next ?? []).entries()) {
      if (!br.goto) {
        errs.push(`node "${id}" next[${i}] has empty goto`);
        continue;
      }
      if (!p.nodes[br.goto]) {
        errs.push(`node "${id}" next[${i}] goto "${br.goto}" does not resolve`);
      }
    }
    // Mirror the Go-side handoff checks (shape only — cross-playbook
    // existence is verified by the editor against its loaded list, not
    // by the structural validator).
    for (const [i, h] of (node.handoff ?? []).entries()) {
      if (!h || !h.trim()) {
        errs.push(`node "${id}" handoff[${i}] is empty`);
        continue;
      }
      if (!/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/.test(h)) {
        errs.push(`node "${id}" handoff[${i}] "${h}" is not a valid playbook id`);
      }
    }
    if ((node.handoff?.length ?? 0) > 0 && !node.terminal_advice) {
      errs.push(`node "${id}" has handoff but no terminal_advice — handoff lives on terminal nodes`);
    }
    // Mirror the Go-side delegate_to checks (shape only — cross-playbook
    // resolution is verified at runtime, not by single-doc validate).
    if (node.delegate_to !== undefined) {
      const dt = node.delegate_to;
      if (typeof dt !== "string" || !/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/.test(dt)) {
        errs.push(`node "${id}" delegate_to "${dt}" is not a valid playbook id`);
      }
      if ((node.next?.length ?? 0) === 0) {
        errs.push(`node "${id}" has delegate_to but no next branches — every delegate node must declare its resume target`);
      }
      if (node.terminal_advice) {
        errs.push(`node "${id}" has both delegate_to and terminal_advice — a delegate node returns to the parent, it cannot also be terminal`);
      }
      if ((node.handoff?.length ?? 0) > 0) {
        errs.push(`node "${id}" has both delegate_to and handoff — a delegate node returns to the parent, it cannot also hand off`);
      }
      if ((node.suggested_calls?.length ?? 0) > 0) {
        errs.push(`node "${id}" has both delegate_to and suggested_calls — a delegate node only delegates; tool calls belong inside the delegated playbook`);
      }
    }
  }
  return errs;
}

// emptyPlaybook returns a minimal valid template the "+ new playbook"
// flow seeds the editor with. The id is intentionally placeholder-y so
// the operator must rename it before saving.
export function emptyPlaybook(): Playbook {
  return {
    id: "new_playbook",
    symptom: "Replace this with the symptom that triggers this playbook",
    description: "",
    entrypoint: "start",
    nodes: {
      start: {
        description: "Replace this with the first investigation step.",
        suggested_calls: [],
        expected_findings: [],
        next: [],
      },
    },
  };
}

// LOCAL_DRAFT_PREFIX is the localStorage key prefix the editor writes
// in-progress edits under. Per-playbook key so two browser tabs editing
// different playbooks don't collide.
export const LOCAL_DRAFT_PREFIX = "triagent.playbook-edit.";
