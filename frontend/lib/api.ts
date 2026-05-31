// Typed wrapper around the Go server's JSON API. Every call goes through
// fetchJSON so cookie-based auth (set by the launch URL's token redirect)
// is preserved and error responses get a consistent shape.

import type {
  AutoModePhase,
  CodefixProposalListing,
  CodefixProposalPayload,
} from "./events";
import type { PlaybookCommit, PlaybookListItem, ToolEntry } from "./playbook";
import type { SyncState, SyncStatePR, SyncStatus } from "./sync-state";
import type { EventEnvelope, Usage } from "./events";
export type { PlaybookCommit, PlaybookListItem, ToolEntry };
export type { SyncState, SyncStatePR, SyncStatus };

export type GHStatus = {
  installed: boolean;
  authenticated: boolean;
  version?: string;
  user?: string;
  reason?: string;
};

export type RepoPathStatus = {
  configured: boolean;
  valid: boolean;
  path?: string;
  // Upstream owner/name slug for the playbooks repo
  // (profile's defaults.playbooks_repo). Empty when the operator
  // hasn't pointed at an upstream — local-only mode. Frontend
  // affordances that push to GitHub gate on this being non-empty.
  repo?: string;
  reason?: string;
};

export type WikiStatus = {
  configured: boolean;
  valid: boolean;
  path?: string;
  repo?: string;
  // subpath is the in-repo dir where the wiki vault lives, when the
  // upstream wiki repo is shared with other artifacts (defaults to
  // "wikis" via the profile). Empty means the wiki tree is at the
  // repo root (legacy flat layout).
  subpath?: string;
  reason?: string;
};

// SessionsStatus mirrors the launcher's view of the upstream-sessions
// clone. Empty `repo` is the canonical "local-only mode" signal — the
// vault still works, but push-as-PR and the peer sessions catalog
// surfaces should be disabled.
export type SessionsStatus = {
  configured: boolean;
  valid: boolean;
  path?: string;
  repo?: string;
  subpath?: string;
  reason?: string;
};

export type ClaudeStatus = {
  installed: boolean;
  version?: string;
  reason?: string;
};

export type CodefixCapability = {
  enabled: boolean;
  hasLinkedRepos: boolean;
  ghAuthenticated: boolean;
  reason?: string;
};

export type Capabilities = {
  gh: GHStatus;
  repoPath: RepoPathStatus;
  wiki: WikiStatus;
  claude: ClaudeStatus;
  sessions: SessionsStatus;
  codefix?: CodefixCapability;
};

// ConnectionStatus is the redacted view of which third-party integrations
// the operator has linked. Tokens are never returned over the wire.
// slack_channel_prefix carries the profile boot config so callers can
// avoid a separate fetch.
export type ConnectionStatus = {
  slack: boolean;
  incidentio: boolean;
  slack_channel_prefix: string;
  // cloud is the read-only list of profile-configured cloud connections,
  // each probed at request time. Configured in the profile, never entered
  // in the panel.
  cloud?: CloudConnection[];
};

// CloudConnection is one read-only cloud source: the alias keying its
// triagent-cloud-<alias> MCP and the request-time identity-probe result. valid
// drives the checkmark; hint is the reauth advice shown when the probe failed.
//
// Each pill renders the same principal + reach shape, with provider-specific
// content. gcp carries assumed_identity (the impersonated service account) and
// projects (its scope allowlist). aws carries source_profile (the operator's SSO
// base) and accounts (the account ids it spans).
export type CloudConnection = {
  alias: string;
  provider: string;
  assumed_identity?: string;
  projects?: string[];
  source_profile?: string;
  accounts?: string[];
  valid: boolean;
  hint?: string;
};

export type SlackChannel = {
  id: string;
  name: string;
  created_unix?: number;
};

// PlaybookTypeItem is the shape returned by GET /api/playbook-types.
// `tracked` reflects whether <name>/type.txt exists at origin/HEAD in
// the system clone — used by the homepage to decide whether deleting
// the type should pop the propose-as-PR modal.
export type PlaybookTypeItem = {
  name: string;
  description: string;
  source: "system" | "user";
  tracked: boolean;
};

// RelatedPlaybookMatch is one entry returned by
// GET /api/playbooks/{id}/related. Mirrors the server-side relatedMatch
// shape (investigate/internal/server/playbooks_related.go).
export type RelatedPlaybookMatch = {
  id: string;
  symptom?: string;
  description?: string;
  type: string;
  score: number;
  match_path: {
    direct?: string[];
    lifted?: { entity: string; via: string }[];
  };
};

// RelatedWikiMatch is one entry returned by
// GET /api/playbooks/{id}/related-wiki. Mirrors the server-side
// relatedWikiMatch shape (investigate/internal/server/playbooks_related_wiki.go).
// No `lifted` shape — wiki entries don't delegate / handoff, so the
// match path only carries direct hits.
export type RelatedWikiMatch = {
  id: string;
  title?: string;
  path: string;
  status?: string;
  severity?: string;
  score: number;
  match_path: {
    direct?: string[];
  };
};

// TagOverrides is the optional query-param shape both correlation
// endpoints accept. When present, the backend uses these as the query
// set INSTEAD of the playbook's saved tags — lets the editor preview
// correlations as the operator edits chip inputs before saving.
export type TagOverrides = {
  services?: string[];
  errors?: string[];
  symptoms?: string[];
};

// PlaybooksUpstreamStatus mirrors the JSON returned by
// GET /api/playbooks-upstream. Lets the playbooks tab render a header
// strip "synced from <repo> · last synced N ago" + decide whether the
// Sync button is actionable.
export type PlaybooksUpstreamStatus = {
  repo: string;
  dir: string;
  gitCheckout: boolean;
  commit?: string;
  lastSynced?: string;
  // Number of upstream commits not yet applied locally. Populated by
  // a quick `git fetch` + `rev-list HEAD..origin/HEAD --count` on
  // every status call. Zero when up-to-date or when the fetch
  // failed (in which case remoteAheadError carries the reason).
  remoteAhead?: number;
  remoteAheadError?: string;
  error?: string;
};

// ── Sessions upstream repo ──────────────────────────────────────────

// SessionsUpstreamStatus mirrors the JSON returned by
// GET /api/sessions-upstream.
export type SessionsUpstreamStatus = {
  repo: string;
  dir: string;
  gitCheckout: boolean;
  commit?: string;
  lastSynced?: string;
  remoteAhead: number;
  remoteAheadError?: string;
  error?: string;
};

export type SessionAuthor = { name: string; email: string };

export type SessionSources = {
  incident_io?: string;
  slack?: string;
  bundle: string;
};

export type SessionCard = {
  slug: string;
  title: string;
  date: string;
  namespace: string;
  author: SessionAuthor;
  sources: SessionSources;
  // Resolver-authoritative drift answer for this slug. The upstream
  // list backend joins each card with the matching local Investigation
  // (or upstream-only when none) so the card carries its own PR pill
  // — frontend doesn't rebuild a slug→PR map.
  syncState: SyncState;
};

export type SessionDoc = {
  slug: string;
  frontmatter: SessionCard & { schema_version: number; id: string };
  body: string;
};

export type SessionPushPRRequest = {
  branch?: string;
  title?: string;
  body?: string;
  base?: string;
};

export type SessionPushPRResult = {
  url: string;
  branch: string;
  base: string;
  slug: string;
};

export type PushPRRequest = {
  yaml: string;
  // Type slot the file lands under in upstream (investigation/,
  // general/, …). Required: the upstream layout is directory-as-type
  // and we can't infer it from the YAML body.
  type: string;
  branch?: string;
  title?: string;
  body?: string;
  base?: string;
};

export type PushPRResult = {
  url: string;
  branch: string;
  base: string;
};

export type Cluster = {
  name: string;
  id: string;
  labels?: Record<string, string>;
};

export type LoginResult = {
  contextName: string;
};

export type PromOverride = {
  service?: string;
  namespace?: string;
  port?: number;
  token?: string;
  disabled?: boolean;
};

// PromDefaults is the shape returned by GET /api/profile/prom-defaults.
// Used to pre-populate the per-investigation prom override form with the
// active profile's configured defaults so operators see what's currently
// wired and only change what they need to.
export type PromDefaults = {
  service: string;
  namespace: string;
  port: number;
};

// InputSchema mirrors the server-side ProfileInput DTO (see Task 4.5).
// Returned by GET /api/profile/inputs; consumed by the dynamic form (Task 5.7).
export type InputSchema = {
  id: string;
  label: string;
  type: "text" | "url" | "textarea" | "cluster_id" | "slack_channel";
  optional: boolean;
  placeholder?: string;
  hint?: string;
};

export type PreflightRequest = {
  // inputs is the new Task-4.4 DTO shape: a map keyed by input ID.
  // Each value is an open Record so type-specific subfields (text, url,
  // channel_id, channel_name, …) can be carried without a union type
  // in the frontend.
  inputs: Record<string, Record<string, unknown>>;
  prom: PromOverride;
  // auto: when true the server's preflight handler also writes an
  // op-mcp.json and the launcher boots an AutoOperator (see T18).
  // Omitted on the wire when false thanks to `auto,omitempty` on the
  // server-side struct.
  auto?: boolean;
};

export type LinkedRepo = {
  owner: string;
  name: string;
  alias?: string;
  description?: string;
  addedAt?: string;
};

// Server-side shape of the cached architecture summary. Mirrors
// repoSummaryResponse in handlers_repo_summary.go. exists=false carries
// hint as a one-line orientation cue ("trigger generation from the
// repo page or wait for auto-gen on connection to complete").
export type RepoSummary = {
  exists: boolean;
  generatedAt?: string;
  kind?: string;
  focus?: string;
  content?: string;
  byteCount?: number;
  error?: string;
  hint?: string;
};

// Lightweight status shape for /summary/status. Frontmatter + in-flight
// state, no body. Used by the sidenav at mount time.
export type RepoSummaryStatus = {
  exists: boolean;
  generatedAt?: string;
  kind?: string;
  byteCount?: number;
  inFlight: boolean;
  error?: string;
};

// Operator-edit diff for a repo: the unified diff between the active
// summary body and its AI-generated baseline. hasEdits=false means the
// active matches the baseline OR no baseline has been written yet (no
// AI regen has happened since the operator hand-authored the file).
export type RepoSummaryEdits = {
  hasEdits: boolean;
  diff?: string;
};


// StreamEnvelope is the wire shape on /api/stream (the multiplex SSE).
// One shape carries every server-emitted event; consumers filter by
// kind + scope id.
export type StreamEnvelope = {
  seq: number;
  kind: string;
  timestamp: string;
  investigationId?: string;
  editorSessionId?: string;
  // Per-kind payload — superset of fields used across all event
  // sources. Optional, narrow to the specific kind in consumers.
  sessionId?: string;
  subtype?: string;
  text?: string;
  toolId?: string;
  toolName?: string;
  toolInput?: Record<string, unknown>;
  parentToolId?: string;
  pushState?: {
    phase: "pushing" | "success" | "error";
    startedAt?: string;
    url?: string;
    branch?: string;
    base?: string;
    error?: string;
  };
  rehydrateState?: RehydrateStatePayload;
  repoSummary?: {
    owner: string;
    name: string;
    phase: "started" | "success" | "error";
    startedAt?: string;
    generatedAt?: string;
    byteCount?: number;
    kind?: string;
    error?: string;
  };
  codefixPRState?: {
    url: string;
    state: "open" | "merged" | "closed" | "draft" | "";
    mergedAt?: string;
    closedAt?: string;
  };
  watchStatus?: {
    watchID: string;
    status: "healthy" | "unhealthy" | "disabled" | string;
    lastPolledAt?: string;
    errorCount?: number;
    running?: number;
    queued?: number;
  };
  signalCreated?: {
    watchID: string;
    signalID: string;
    outcome: string;
    investigationID?: string;
    manuallyStarted?: boolean;
  };
  itemCaptured?: {
    watchID: string;
    itemID: string;
    signalID?: string;
    filtered?: boolean;
  };
  ingestRunStarted?: {
    watchID: string;
    runID: string;
    startedAt: string;
    itemCount: number;
  };
  ingestRunFinished?: {
    watchID: string;
    runID: string;
    status: string;
    durationMs: number;
    error?: string;
  };
  // usage rides on assistant + result envelopes — claude's per-message
  // and per-CLI-invocation token tallies. Subscribers that care about
  // running session totals (Sidebar, SessionView footer) sum costUsd +
  // usage on result envelopes; assistant-level usage is informational.
  usage?: Usage;
  // costUsd rides on result envelopes only. See usage above.
  costUsd?: number;
};

export type TranscriptResponse<T> = {
  events: T[];
  lastSeq: number;
};

export type Investigation = {
  id: string;
  namespace: string;
  incidentUrl?: string;
  slackChannelUrl?: string;
  notes?: string;
  label?: string;
  mcpConfigPath: string;
  docsPrefix?: string;
  sessionDir: string;
  promEnabled: boolean;
  promUrl?: string;
  slackMCPEnabled?: boolean;
  incidentioMCPEnabled?: boolean;
  linkedRepos?: LinkedRepo[];
  createdAt: string;
  started: boolean;
  streaming: boolean;
  archived: boolean;
  resumable: boolean;
  importedFrom?: ImportedFrom;
  importedAt?: string;
  originatingSignal?: OriginatingSignal;
  author?: SessionAuthor;
  pushed?: boolean;
  pushedAt?: string;
  // GitHub PR URL set when this session was pushed upstream. Used by
  // the SessionView's archived banner to render a clickable "pushed ↗"
  // link instead of plain text.
  pushUrl?: string;
  // Most recent PR lifecycle state from `gh pr list`. Empty when we
  // haven't refreshed yet (e.g. just pushed and the next refresh
  // hasn't run) or when the PR isn't visible to gh.
  prState?: "open" | "merged" | "closed";
  prMergedAt?: string;
  prClosedAt?: string;
  // True while a kicked-off push goroutine is running on the server.
  // Survives refresh: persisted to metadata.json. The SessionView uses
  // it on mount to restore the pending toast and disable the button.
  pushInProgress?: boolean;
  // ISO timestamp the goroutine started. Available for UIs that want
  // to render elapsed-time while pushInProgress is true.
  pushStartedAt?: string;
  // Last failed-push message. Cleared on the next kick-off / on
  // success. Surfaces in the SessionView when the operator opens a
  // session whose previous push errored.
  pushError?: string;
  // Canonical sessions-repo slug for this investigation (matches the
  // upstream layout sessions/<YYYY-MM>/<slug>/). Lets the upstream
  // browse view detect when an upstream session has a local
  // counterpart so the "replay" button can route to it instead of
  // creating a duplicate import.
  slug?: string;
  // Resolver-authoritative drift answer (mirror of Go's
  // sessionSyncStateFor). Sidebar checkmark, ImportedFromBadge gate,
  // and any future audit tool read this rather than recomputing
  // their own (Pushed && PRState !== "closed") variants. Backend
  // populates it on Snapshot — see internal/server/sync_state.go.
  syncState: SyncState;
  // Auto holds the auto-mode lifecycle state. Mirrors Go's
  // auto.State (investigate/internal/auto/persist.go). Absent on
  // legacy investigations that pre-date the field; consumers should
  // treat `undefined` as "auto mode never enabled". The Sidebar
  // reads `auto.enabled` to gate the bot glyph and `auto.phase` to
  // pick its colour.
  auto?: {
    enabled: boolean;
    phase?: AutoModePhase;
  };
  // usage is the running per-investigation token total summed from
  // every result envelope claude emitted across the session. Absent
  // until the first result lands. Sidebar + chat footer render
  // formatTokens(totalTokens(usage)).
  usage?: Usage;
  // costUsd is the running total_cost_usd summed from result
  // envelopes. Absent until the first result lands.
  costUsd?: number;
};

// ImportedFrom is the provenance block on sessions adopted from a teammate's
// share bundle. Native sessions have it absent.
export type ImportedFrom = {
  investigationId?: string;
  namespace: string;
  incidentUrl?: string;
  slackChannelUrl?: string;
  notes?: string;
  label?: string;
  createdAt?: string;
};

// OriginatingSignal points an investigation back to the signal in the
// watch that produced it. Set when the launcher spawns the
// investigation from a watch (autoStart or manual). Absent for
// preflight-created investigations.
export type OriginatingSignal = {
  watchID: string;
  signalID: string;
};

// ── wiki proposals ──────────────────────────────────────────────────

// Approve now commits locally only; no PR is opened.
export type WikiProposalApprovedResponse = {
  ok: true;
  slug: string;
  path: string;         // vault-relative path, e.g. "entries/<slug>.md"
  commit: string;       // short sha of the local commit
  stubs_created?: string[];
};

// WikiPushPRRequest/Response for POST /api/wiki/entries/{slug}/push-pr
export type WikiPushPRRequest = {
  branch?: string;
  title?: string;
  body?: string;
  base?: string;
};

export type WikiPushPRResponse = {
  ok: true;
  url: string;
  branch: string;
  base: string;
  slug: string;
};

export type WikiPushPRErrorResponse = {
  ok: false;
  error?: string;
  errors?: string[];
};

export type WikiProposalApproveErrorResponse = {
  ok: false;
  error?: string;
  errors?: string[];
};

// WikiProposalGetResponse covers all three states the GET endpoint
// can return for a wiki proposal id: the pending draft (status =
// "pending" with new_md/slug), or a resolution outcome
// (status = "approved" with vault commit details, or "declined").
export type WikiProposalGetResponse = {
  proposal_id: string;
  status?: "pending" | "approved" | "declined";
  // pending fields
  slug?: string;
  new_md?: string;
  base_md?: string;
  is_new?: boolean;
  new_entities?: WikiProposalNewEntity[];
  // approved fields (when status === "approved")
  path?: string;
  commit?: string;
  stubs_created?: string[];
  // resolution timestamp (when status is approved/declined)
  at?: string;
};

// WikiProposalNewEntity mirrors mcp/internal/wiki.NewEntityStub. The
// proposal card surfaces raw_md in its raw view so the operator can
// audit every file the approve flow will write.
export type WikiProposalNewEntity = {
  type: string;
  name: string;
  description?: string;
  raw_md: string;
};

// PlaybookProposalListItem mirrors handlers_proposals.go::playbookProposalListItem.
// The playbooks sidenav reads this to surface pending proposals;
// clicking a row opens the editor and auto-switches to the AI proposal
// tab.
export type PlaybookProposalListItem = {
  proposal_id: string;
  playbook_id: string;
  type: string;
  description?: string;
  is_new: boolean;
  modified_at: string;
};

export type PlaybookProposalListResponse = {
  proposals: PlaybookProposalListItem[];
};

// PlaybookProposalGetResponse mirrors WikiProposalGetResponse but
// covers the playbook side. status === "pending" means the draft is
// still on disk and the chat card may render Approve/Decline. The
// approved/declined statuses come from the resolution ledger written
// by the approve/decline handlers.
export type PlaybookProposalGetResponse = {
  proposal_id: string;
  status?: "pending" | "approved" | "declined";
  // pending fields
  playbook_id?: string;
  base_yaml?: string;
  new_yaml?: string;
  // approved fields
  id?: string;
  type?: string;
  version?: string;
  activated?: boolean;
  // resolution timestamp
  at?: string;
};

export type MCPCallStats = {
  alias: string;
  lastSeen?: string;     // RFC3339
  lastSuccess?: string;
  lastErrorAt?: string;
  lastErrorMsg?: string;
  calls: number;
  errors: number;
  inFlight: number;
};

export type MCPHealthResponse = {
  servers: MCPCallStats[];
};

export type MCPProbeResult = {
  alias: string;
  kind: string;
  ok: boolean;
  reason?: string;
  latencyMs: number;
  checkedAt: string;
};

export type MCPProbeResponse = {
  servers: MCPProbeResult[];
};

export type RehydratePhase = "started" | "succeeded" | "failed";

export interface RehydrateStatePayload {
  phase: RehydratePhase;
  error?: string;
  degraded?: string[];
}

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function fetchJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    credentials: "include",
    headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
    ...init,
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = await res.json();
      if (body?.error) msg = body.error;
    } catch {
      /* keep status text */
    }
    throw new ApiError(res.status, msg);
  }
  return (await res.json()) as T;
}

// filenameFromContentDisposition pulls the suggested download filename out
// of a Content-Disposition header. Returns null if absent / unparseable;
// callers should fall back to a sensible default.
function filenameFromContentDisposition(header: string | null): string | null {
  if (!header) return null;
  const m = /filename\*?=(?:UTF-8'')?"?([^";]+)"?/i.exec(header);
  return m ? decodeURIComponent(m[1]) : null;
}

async function postEmpty(path: string): Promise<void> {
  const res = await fetch(path, { method: "POST", credentials: "include" });
  if (!res.ok && res.status !== 202) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = await res.json();
      if (body?.error) msg = body.error;
    } catch {
      /* keep status text */
    }
    throw new ApiError(res.status, msg);
  }
}

export const api = {
  // getProfileInputs fetches the operator's active profile input schema.
  // Used by the dynamic investigation form (Task 5.7) to render the correct
  // fields. Calls GET /api/profile/inputs (backend Task 4.5).
  getProfileInputs: () =>
    fetchJSON<{ inputs: InputSchema[] }>("/api/profile/inputs").then(
      (r) => r.inputs,
    ),

  // getProfilePromDefaults fetches the active profile's prometheus defaults
  // so the investigation form can pre-populate the prom override panel.
  // 503 when no profile is configured (returns null via the catch below).
  getProfilePromDefaults: () =>
    fetchJSON<PromDefaults>("/api/profile/prom-defaults").catch(() => null),

  health: () => fetchJSON<{ ok: boolean; version?: string }>("/api/health"),

  listClusters: () =>
    fetchJSON<{ clusters: Cluster[] }>("/api/clusters").then((r) => r.clusters),

  login: (cluster: string) =>
    fetchJSON<LoginResult>("/api/login", {
      method: "POST",
      body: JSON.stringify({ cluster }),
    }),

  preflight: (req: PreflightRequest) =>
    fetchJSON<Investigation>("/api/preflight", {
      method: "POST",
      body: JSON.stringify(req),
    }),

  listInvestigations: () =>
    fetchJSON<{ investigations: Investigation[] }>("/api/investigations").then(
      (r) => r.investigations,
    ),

  getInvestigation: (id: string) =>
    fetchJSON<Investigation>(`/api/investigations/${id}`),

  deleteInvestigation: async (id: string) => {
    const res = await fetch(`/api/investigations/${id}`, {
      method: "DELETE",
      credentials: "include",
    });
    if (!res.ok && res.status !== 204) {
      let msg = `${res.status} ${res.statusText}`;
      try {
        const body = await res.json();
        if (body?.error) msg = body.error;
      } catch {
        /* keep status */
      }
      throw new ApiError(res.status, msg);
    }
  },

  // exportInvestigation downloads the share bundle for an investigation as
  // a Blob alongside the suggested filename the server picked from
  // Content-Disposition. The caller is responsible for triggering the
  // browser download — this surface is shared between the header button
  // and any future "copy to clipboard as JSON" affordance.
  exportInvestigation: async (
    id: string,
  ): Promise<{ blob: Blob; filename: string }> => {
    const res = await fetch(`/api/investigations/${id}/export`, {
      credentials: "include",
    });
    if (!res.ok) {
      let msg = `${res.status} ${res.statusText}`;
      try {
        const body = await res.json();
        if (body?.error) msg = body.error;
      } catch {
        /* keep status */
      }
      throw new ApiError(res.status, msg);
    }
    const blob = await res.blob();
    const filename =
      filenameFromContentDisposition(res.headers.get("Content-Disposition")) ??
      `${id}.triagent.json`;
    return { blob, filename };
  },

  // importInvestigation uploads a share bundle as multipart/form-data so
  // the same code path works for the file picker, drag-drop, and a future
  // CLI/IDE entry point. Returns the freshly-adopted (archived)
  // investigation.
  importInvestigation: async (file: Blob): Promise<Investigation> => {
    const form = new FormData();
    form.append("file", file);
    const res = await fetch("/api/investigations/import", {
      method: "POST",
      credentials: "include",
      body: form,
    });
    if (!res.ok) {
      let msg = `${res.status} ${res.statusText}`;
      try {
        const body = await res.json();
        if (body?.error) msg = body.error;
      } catch {
        /* keep status */
      }
      throw new ApiError(res.status, msg);
    }
    return (await res.json()) as Investigation;
  },

  startSession: (id: string) => postEmpty(`/api/investigations/${id}/start`),

  // interruptInvestigation cancels the in-flight claude turn. Mirror of
  // ChatGPT's stop button: the composer replaces Send with Stop while
  // the agent is streaming, clicking Stop posts here. Server responds
  // 202 on success, 409 when nothing is in flight (idempotent-ish on
  // the client side — caller can ignore the 409 to avoid racing the
  // end-of-turn envelope).
  interruptInvestigation: (id: string) =>
    postEmpty(`/api/investigations/${id}/interrupt`),

  sendMessage: (id: string, text: string) =>
    fetch(`/api/investigations/${id}/messages`, {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text }),
    }).then(async (res) => {
      if (!res.ok && res.status !== 202) {
        let msg = `${res.status} ${res.statusText}`;
        try {
          const body = await res.json();
          if (body?.error) msg = body.error;
        } catch {
          /* keep */
        }
        throw new ApiError(res.status, msg);
      }
    }),

  // getMCPHealth returns the live per-MCP-server call stats for an active
  // investigation. Polled by MCPStatusBar every 5s.
  getMCPHealth: (invID: string): Promise<MCPHealthResponse> =>
    fetchJSON(`/api/investigations/${encodeURIComponent(invID)}/mcp-health`),

  // getMCPProbe runs active readiness checks against the per-session MCP
  // servers' dependencies. Polled by MCPStatusBar every 30s. Independent
  // of telemetry — answers "reachable right now?" rather than "used recently?".
  getMCPProbe: (invID: string): Promise<MCPProbeResponse> =>
    fetchJSON(`/api/investigations/${encodeURIComponent(invID)}/mcp-probe`),

  listRepos: () =>
    fetchJSON<{ defaults: LinkedRepo[]; user: LinkedRepo[] }>("/api/repos"),

  addRepo: (repo: { owner: string; name: string; alias?: string; description: string }) =>
    fetchJSON<LinkedRepo & { summaryStatus?: string }>("/api/repos", {
      method: "POST",
      body: JSON.stringify(repo),
    }),

  removeRepo: async (owner: string, name: string) => {
    const res = await fetch(`/api/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`, {
      method: "DELETE",
      credentials: "include",
    });
    if (!res.ok && res.status !== 204) {
      let msg = `${res.status} ${res.statusText}`;
      try {
        const body = await res.json();
        if (body?.error) msg = body.error;
      } catch {
        /* keep status */
      }
      throw new ApiError(res.status, msg);
    }
  },

  getRepoSummary: (owner: string, name: string): Promise<RepoSummary> =>
    fetchJSON<RepoSummary>(
      `/api/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}/summary`,
    ),

  getRepoSummaryStatus: (
    owner: string,
    name: string,
  ): Promise<RepoSummaryStatus> =>
    fetchJSON<RepoSummaryStatus>(
      `/api/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}/summary/status`,
    ),

  // getRepoSummaryEdits returns the unified diff of operator hand-edits
  // against the AI-generated baseline. Used to render the "operator
  // edits • view diff" badge and the view-diff modal on the repo page,
  // and to drive the three-option confirm dialog when the operator
  // triggers a refresh over a hand-edited summary.
  getRepoSummaryEdits: (
    owner: string,
    name: string,
  ): Promise<RepoSummaryEdits> =>
    fetchJSON<RepoSummaryEdits>(
      `/api/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}/summary/edits`,
    ),

  refreshRepoSummary: (
    owner: string,
    name: string,
    opts?: { kind?: string; focus?: string; includeOperatorEdits?: boolean },
  ): Promise<{ status: string }> =>
    fetchJSON<{ status: string }>(
      `/api/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}/summary/refresh`,
      {
        method: "POST",
        body: JSON.stringify(opts ?? {}),
      },
    ),

  updateRepoSummary: (
    owner: string,
    name: string,
    body: { content: string; kind?: string },
  ): Promise<RepoSummaryStatus> =>
    fetchJSON<RepoSummaryStatus>(
      `/api/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}/summary`,
      {
        method: "PUT",
        body: JSON.stringify(body),
      },
    ),

  // streamURL / closeStreamURL are the multiplex SSE endpoints — one
  // connection per browser tab that fans out all scoped events. The
  // caller owns the EventSource lifecycle and passes a connToken so the
  // server can free the slot on an explicit close beacon.
  streamURL: (connToken: string) =>
    `/api/stream?conn=${encodeURIComponent(connToken)}`,
  closeStreamURL: (connToken: string) =>
    `/api/stream/close?conn=${encodeURIComponent(connToken)}`,

  // getInvestigationTranscript fetches the event backlog for a given
  // investigation id, returning all events and the highest sequence
  // number seen. Used to replay history after connecting to the
  // multiplex stream.
  getInvestigationTranscript: (id: string): Promise<TranscriptResponse<EventEnvelope>> =>
    fetchJSON(`/api/investigations/${encodeURIComponent(id)}/transcript`),

  // getEditorTranscript fetches the event backlog for a given editor
  // session id, returning all events and the highest sequence number
  // seen. Used to replay history after connecting to the multiplex stream.
  getEditorTranscript: (id: string): Promise<TranscriptResponse<EventEnvelope>> =>
    fetchJSON(`/api/editor-sessions/${encodeURIComponent(id)}/transcript`),

  // ── Connections (Slack / incident.io tokens) ──────────────────────────
  getConnections: () =>
    fetchJSON<ConnectionStatus>("/api/connections"),

  putSlackToken: (token: string) =>
    fetchJSON<ConnectionStatus>("/api/connections/slack", {
      method: "PUT",
      body: JSON.stringify({ token }),
    }),

  putIncidentioToken: (token: string) =>
    fetchJSON<ConnectionStatus>("/api/connections/incidentio", {
      method: "PUT",
      body: JSON.stringify({ token }),
    }),

  clearConnection: (kind: "slack" | "incidentio") =>
    fetchJSON<ConnectionStatus>(`/api/connections/${kind}`, {
      method: "DELETE",
    }),

  listSlackChannels: () =>
    fetchJSON<SlackChannel[]>("/api/slack/channels"),

  // ── Playbook editor surface ───────────────────────────────────────────
  listPlaybooks: () =>
    fetchJSON<{ playbooks: PlaybookListItem[] }>("/api/playbooks").then(
      (r) => r.playbooks,
    ),

  getPlaybook: (id: string) =>
    fetchJSON<PlaybookListItem>(`/api/playbooks/${encodeURIComponent(id)}`),

  // getRelatedPlaybooks returns up to 5 playbooks correlated with the
  // queried playbook's own entity tags (services / errors / symptoms).
  // The queried playbook is filtered out of the results. Returns an
  // empty array when the playbook has no entity tags or no neighbours
  // share any tags. Throws ApiError on 404 (unknown id) or network
  // failure.
  //
  // When `overrides` is set, those tags are sent as repeated
  // ?services=&errors=&symptoms= query params and the backend uses
  // them INSTEAD of the playbook's saved tags. Lets the editor preview
  // correlations as the operator edits the chip inputs before save.
  getRelatedPlaybooks: (id: string, overrides?: TagOverrides) =>
    fetchJSON<{ related: RelatedPlaybookMatch[] }>(
      `/api/playbooks/${encodeURIComponent(id)}/related${buildTagOverrideQS(overrides)}`,
    ).then((r) => r.related ?? []),

  // getRelatedWikiEntries returns up to 5 wiki entries correlated with
  // the queried playbook's entity tags. Mirrors getRelatedPlaybooks
  // but targets the wiki vault (incidents / past investigations).
  // Empty when the playbook has no tags or no overlap. Throws on 404
  // (unknown id) or network failure.
  //
  // Accepts the same `overrides` shape for live-preview during edits.
  getRelatedWikiEntries: (id: string, overrides?: TagOverrides) =>
    fetchJSON<{ related: RelatedWikiMatch[] }>(
      `/api/playbooks/${encodeURIComponent(id)}/related-wiki${buildTagOverrideQS(overrides)}`,
    ).then((r) => r.related ?? []),

  // savePlaybook returns null on success; on a 400 with structured
  // validation errors it returns them as a string array so YamlPanel
  // can render them inline. Other errors throw an ApiError.
  // The type slot is required — directory-as-type means the launcher
  // routes the file under <userdir>/<type>/<id>.yaml.
  // message is the optional commit message; the backend auto-generates
  // one when absent.
  savePlaybook: async (
    id: string,
    body: string,
    type: string,
    active: boolean,
    message?: string,
  ): Promise<string[] | null> => {
    const res = await fetch(`/api/playbooks/${encodeURIComponent(id)}`, {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ yaml: body, type, active, message }),
    });
    if (res.ok || res.status === 204) return null;
    if (res.status === 400) {
      try {
        const err = await res.json();
        if (Array.isArray(err?.errors)) return err.errors as string[];
      } catch {
        /* fall through */
      }
    }
    let msg = `${res.status} ${res.statusText}`;
    try {
      const b = await res.json();
      if (b?.error) msg = b.error;
    } catch {
      /* keep */
    }
    throw new ApiError(res.status, msg);
  },

  deletePlaybook: async (id: string) => {
    const res = await fetch(`/api/playbooks/${encodeURIComponent(id)}`, {
      method: "DELETE",
      credentials: "include",
    });
    if (!res.ok && res.status !== 204) {
      let msg = `${res.status} ${res.statusText}`;
      try {
        const body = await res.json();
        if (body?.error) msg = body.error;
      } catch {
        /* keep */
      }
      throw new ApiError(res.status, msg);
    }
  },

  listTools: () =>
    fetchJSON<{ tools: ToolEntry[] }>("/api/tools").then((r) => r.tools),

  getCapabilities: () => fetchJSON<Capabilities>("/api/capabilities"),

  // previewPlaybookSyncState asks the launcher "would this draft body
  // be a no-op push against upstream?". Used by the editor to gate the
  // push-PR button in real time as the operator types — same resolver
  // the list endpoint uses, so the two views never disagree about
  // drift. Empty yaml falls back to the saved file (cheap refresh
  // after save). Throws on network/server error; callers should
  // degrade gracefully (e.g. leave the badge in its previous state).
  previewPlaybookSyncState: (
    id: string,
    yaml: string,
    type: string,
  ): Promise<SyncState> =>
    fetchJSON<SyncState>(
      `/api/playbooks/${encodeURIComponent(id)}/sync-state`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ yaml, type }),
      },
    ),

  // ── Commit-based history (replaces version endpoints) ────────────────
  // getPlaybookAtCommit fetches the YAML body at a specific git commit.
  // Used by CommitsDropdown when the operator picks a historical entry.
  getPlaybookAtCommit: (id: string, sha: string) =>
    fetchJSON<{ id: string; sha: string; yaml: string; source: "local" | "upstream" }>(
      `/api/playbooks/${encodeURIComponent(id)}/commits/${encodeURIComponent(sha)}`,
    ),

  // listPlaybookCommits fetches the paged commit history for a playbook.
  // The initial 50 are embedded in the detail response; call this with
  // `before` set to the oldest known sha to load older entries.
  listPlaybookCommits: (id: string, before?: string) => {
    const qs = before ? `?before=${encodeURIComponent(before)}` : "";
    return fetchJSON<{ commits: PlaybookCommit[] }>(
      `/api/playbooks/${encodeURIComponent(id)}/commits${qs}`,
    ).then((r) => r.commits);
  },

  // ── Playbook types ───────────────────────────────────────────────────
  // Type slots are directories under the upstream playbooks clone.
  // listPlaybookTypes reads them; createPlaybookType writes a new
  // <type>/type.txt and (if gh + git are wired) opens a PR against the
  // upstream so the team picks the new slot up on next sync.
  listPlaybookTypes: () =>
    fetchJSON<{ types: PlaybookTypeItem[] }>("/api/playbook-types").then(
      (r) => r.types,
    ),

  createPlaybookType: async (name: string, description: string) => {
    const res = await fetch("/api/playbook-types", {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name, description }),
    });
    if (!res.ok) {
      let msg = `${res.status} ${res.statusText}`;
      try {
        const b = await res.json();
        if (b?.error) msg = b.error;
      } catch {
        /* keep */
      }
      throw new ApiError(res.status, msg);
    }
    return (await res.json()) as {
      ok: boolean;
      name: string;
      description: string;
      prUrl?: string;
      pushWarning?: string;
    };
  },

  // deletePlaybookType removes the local <type>/ dir + type.txt. The
  // server rejects with 409 if the dir still contains anything other
  // than type.txt. No git operations — if the type also exists at
  // origin/HEAD, the next sync will restore it.
  deletePlaybookType: async (name: string): Promise<void> => {
    const res = await fetch(
      `/api/playbook-types/${encodeURIComponent(name)}`,
      { method: "DELETE", credentials: "include" },
    );
    if (!res.ok) {
      let msg = `${res.status} ${res.statusText}`;
      try {
        const b = await res.json();
        if (b?.error) msg = b.error;
      } catch {
        /* keep */
      }
      throw new ApiError(res.status, msg);
    }
  },

  // proposePlaybookTypeRemoval cuts a branch off origin/main with
  // <type>/ removed, pushes, and opens a PR via gh. Push/gh failure is
  // non-fatal — the response carries `pushWarning` and the local
  // working tree is on the new branch with the dir gone.
  proposePlaybookTypeRemoval: async (name: string) => {
    const res = await fetch(
      `/api/playbook-types/${encodeURIComponent(name)}/propose-removal`,
      { method: "POST", credentials: "include" },
    );
    if (!res.ok) {
      let msg = `${res.status} ${res.statusText}`;
      try {
        const b = await res.json();
        if (b?.error) msg = b.error;
      } catch {
        /* keep */
      }
      throw new ApiError(res.status, msg);
    }
    return (await res.json()) as {
      ok: boolean;
      name: string;
      prUrl?: string;
      pushWarning?: string;
    };
  },

  // ── Upstream playbooks repo ─────────────────────────────────────────
  // getPlaybooksUpstream returns metadata about the launcher's clone
  // of the upstream playbooks repo: its OWNER/REPO, dir, commit, and
  // the last-synced timestamp (HEAD's committer date). The playbooks
  // tab uses this to render a "synced from <repo> · N ago" hint and
  // to gate the Sync button (disabled when the clone isn't a git
  // checkout — e.g. operator pre-seeded the dir manually).
  getPlaybooksUpstream: () =>
    fetchJSON<PlaybooksUpstreamStatus>("/api/playbooks-upstream"),

  syncPlaybooksUpstream: async () => {
    const res = await fetch("/api/playbooks-upstream/sync", {
      method: "POST",
      credentials: "include",
    });
    if (!res.ok) {
      let msg = `${res.status} ${res.statusText}`;
      try {
        const b = await res.json();
        if (b?.error) msg = b.error;
      } catch {
        /* keep */
      }
      throw new ApiError(res.status, msg);
    }
    return (await res.json()) as { commit?: string; lastSynced?: string };
  },

  // ── Playbook proposals (chat diff card) ──────────────────────────────
  // listPlaybookProposals enumerates pending playbook proposals on disk
  // so the sidenav can surface them on /playbooks.
  listPlaybookProposals: () =>
    fetchJSON<PlaybookProposalListResponse>("/api/playbook-proposals"),

  // getPlaybookProposal fetches the current state of a proposal so
  // chat surfaces re-mounting after a page reload can render the
  // outcome (approved/declined) instead of stale Approve/Decline
  // buttons that would 404 on click.
  getPlaybookProposal: async (
    proposalID: string,
  ): Promise<PlaybookProposalGetResponse> => {
    return fetchJSON<PlaybookProposalGetResponse>(
      `/api/playbook-proposals/${encodeURIComponent(proposalID)}`,
    );
  },

  // approvePlaybookProposal promotes a draft: writes the playbook file
  // and activates it. Returns the id on success, or structured
  // validation errors on 400.
  approvePlaybookProposal: async (
    proposalID: string,
  ): Promise<
    | { ok: true; id: string; version: string; activated: boolean }
    | { ok: false; errors: string[] }
  > => {
    const res = await fetch(
      `/api/playbook-proposals/${encodeURIComponent(proposalID)}/approve`,
      {
        method: "POST",
        credentials: "include",
      },
    );
    if (res.ok) {
      const body = (await res.json()) as {
        id: string;
        version: string;
        activated?: boolean;
      };
      return {
        ok: true,
        id: body.id,
        version: body.version,
        activated: body.activated ?? true,
      };
    }
    if (res.status === 400) {
      try {
        const err = await res.json();
        if (Array.isArray(err?.errors))
          return { ok: false, errors: err.errors as string[] };
      } catch {
        /* fall through */
      }
    }
    let msg = `${res.status} ${res.statusText}`;
    try {
      const b = await res.json();
      if (b?.error) msg = b.error;
    } catch {
      /* keep */
    }
    throw new ApiError(res.status, msg);
  },

  // declinePlaybookProposal drops the draft. Idempotent on the server
  // side; safe to call twice if the UI doesn't track state perfectly.
  // When note is supplied, the server persists it in the resolution
  // marker so the strategies MCP's list_proposals tool can surface the
  // operator's refinement to dispatched sub-agents on subsequent
  // walks. The chat-message follow-up that ProposalCard sends is
  // independent — the marker is the durable record for sub-agent
  // reads, chat is the master agent's in-conversation context.
  declinePlaybookProposal: async (
    proposalID: string,
    note?: string,
  ): Promise<void> => {
    const init: RequestInit = { method: "POST", credentials: "include" };
    if (note && note.trim() !== "") {
      init.headers = { "Content-Type": "application/json" };
      init.body = JSON.stringify({ note: note.trim() });
    }
    const res = await fetch(
      `/api/playbook-proposals/${encodeURIComponent(proposalID)}/decline`,
      init,
    );
    if (!res.ok && res.status !== 204) {
      let msg = `${res.status} ${res.statusText}`;
      try {
        const b = await res.json();
        if (b?.error) msg = b.error;
      } catch {
        /* keep */
      }
      throw new ApiError(res.status, msg);
    }
  },

  // pushPlaybookPR returns the PR URL on success; on a 400 with
  // structured validation errors, returns them as a string array; other
  // errors throw.
  pushPlaybookPR: async (
    id: string,
    req: PushPRRequest,
  ): Promise<{ ok: true; result: PushPRResult } | { ok: false; errors: string[] }> => {
    const res = await fetch(`/api/playbooks/${encodeURIComponent(id)}/push-pr`, {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(req),
    });
    if (res.ok) {
      return { ok: true, result: (await res.json()) as PushPRResult };
    }
    if (res.status === 400) {
      try {
        const err = await res.json();
        if (Array.isArray(err?.errors))
          return { ok: false, errors: err.errors as string[] };
      } catch {
        /* fall through */
      }
    }
    let msg = `${res.status} ${res.statusText}`;
    try {
      const b = await res.json();
      if (b?.error) msg = b.error;
    } catch {
      /* keep */
    }
    throw new ApiError(res.status, msg);
  },

  // ── Editor chat sessions ─────────────────────────────────────────────
  // The drawer at the bottom of the playbook editor (and the wiki
  // editor) talks to a cluster-less authoring agent. Sessions are
  // frozen to one Subject at create time; the manager closes any
  // prior session for the same Subject.Key() when a new one is
  // opened. The "X" button on the drawer header maps to
  // deleteEditorSession; collapsing the drawer does NOT terminate the
  // session — operator can re-open and resume.
  createOrResumeEditorSession: (
    subject: EditorSubject,
    sources?: EditorSources,
  ) =>
    fetchJSON<EditorSession>("/api/editor-sessions", {
      method: "POST",
      body: JSON.stringify({ subject, sources }),
    }),

  findEditorSession: async (
    subject: EditorSubject,
  ): Promise<EditorSession | null> => {
    const url = `/api/editor-sessions?subject_kind=${encodeURIComponent(
      subject.kind,
    )}&subject_key=${encodeURIComponent(subjectKey(subject))}`;
    const res = await fetch(url, { credentials: "include" });
    if (res.status === 404) return null;
    if (!res.ok) {
      let msg = `${res.status} ${res.statusText}`;
      try {
        const b = await res.json();
        if (b?.error) msg = b.error;
      } catch {
        /* keep */
      }
      throw new ApiError(res.status, msg);
    }
    return (await res.json()) as EditorSession;
  },

  deleteEditorSession: async (id: string) => {
    const res = await fetch(`/api/editor-sessions/${encodeURIComponent(id)}`, {
      method: "DELETE",
      credentials: "include",
    });
    if (!res.ok && res.status !== 204) {
      let msg = `${res.status} ${res.statusText}`;
      try {
        const b = await res.json();
        if (b?.error) msg = b.error;
      } catch {
        /* keep */
      }
      throw new ApiError(res.status, msg);
    }
  },

  rebindEditorSession: async (
    id: string,
    subject: EditorSubject,
  ): Promise<EditorSession> => {
    const res = await fetch(
      `/api/editor-sessions/${encodeURIComponent(id)}/rebind`,
      {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(subject),
      },
    );
    if (!res.ok) {
      let msg = `${res.status} ${res.statusText}`;
      try {
        const b = await res.json();
        if (b?.error) msg = b.error;
      } catch {
        /* keep */
      }
      throw new ApiError(res.status, msg);
    }
    return (await res.json()) as EditorSession;
  },

  sendEditorMessage: (id: string, text: string) =>
    fetch(`/api/editor-sessions/${encodeURIComponent(id)}/messages`, {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text }),
    }).then(async (res) => {
      if (!res.ok && res.status !== 202) {
        let msg = `${res.status} ${res.statusText}`;
        try {
          const body = await res.json();
          if (body?.error) msg = body.error;
        } catch {
          /* keep */
        }
        throw new ApiError(res.status, msg);
      }
    }),

  // ── Wiki proposals (chat diff card) ─────────────────────────────────
  // approveWikiProposal promotes a draft into the wiki vault and opens
  // a PR. Returns the structured success payload (ok=true with PR URL)
  // or a structured error (ok=false with details).
  approveWikiProposal: async (
    proposalID: string,
  ): Promise<
    WikiProposalApprovedResponse | WikiProposalApproveErrorResponse
  > => {
    return fetchJSON<
      WikiProposalApprovedResponse | WikiProposalApproveErrorResponse
    >(
      `/api/wiki-proposals/${encodeURIComponent(proposalID)}/approve`,
      { method: "POST" },
    );
  },

  // declineWikiProposal drops the draft from the local proposals dir.
  // Idempotent on the server. When note is supplied, the server
  // persists it in the resolution marker so future dispatched
  // sub-agents (via the dispatch prompt's auto-injected proposal
  // state) read the operator's pushback instead of re-submitting the
  // same shape. Symmetric to declinePlaybookProposal.
  declineWikiProposal: async (
    proposalID: string,
    note?: string,
  ): Promise<void> => {
    const init: RequestInit = { method: "POST", credentials: "include" };
    if (note && note.trim() !== "") {
      init.headers = { "Content-Type": "application/json" };
      init.body = JSON.stringify({ note: note.trim() });
    }
    await fetch(
      `/api/wiki-proposals/${encodeURIComponent(proposalID)}/decline`,
      init,
    );
  },

  // getWikiProposal fetches the draft markdown for a refresh / late
  // mount of WikiProposalCard if the agent's tool result wasn't around
  // (e.g. page reload mid-review).
  getWikiProposal: async (
    proposalID: string,
  ): Promise<WikiProposalGetResponse> => {
    return fetchJSON<WikiProposalGetResponse>(
      `/api/wiki-proposals/${encodeURIComponent(proposalID)}`,
    );
  },

  // getCodefixProposal fetches the persisted CodefixProposalPayload
  // joined with current pr_state. Used by CodefixProposalCard on mount
  // (and again as a refresh if the agent's tool result wasn't around,
  // e.g. page reload mid-review).
  getCodefixProposal: async (
    proposalID: string,
  ): Promise<CodefixProposalPayload & { pr_state?: string }> => {
    return fetchJSON<CodefixProposalPayload & { pr_state?: string }>(
      `/api/codefix-proposals/${encodeURIComponent(proposalID)}`,
    );
  },

  // listCodefixProposals returns every on-disk proposal joined with
  // current pr_state. Optional repo filter scopes the response to a
  // single owner/name. The repos page sidenav reads this for its
  // activity panel.
  listCodefixProposals: async (
    repo?: string,
  ): Promise<CodefixProposalListing[]> => {
    const qs = repo ? `?repo=${encodeURIComponent(repo)}` : "";
    const r = await fetchJSON<{ proposals: CodefixProposalListing[] }>(
      `/api/codefix-proposals${qs}`,
    );
    return r.proposals;
  },

  // discardCodefixProposal closes the draft PR + deletes the remote
  // branch + writes the discard ledger. 409 on the server means the
  // ledger already exists (idempotent); we treat that as success on
  // the client because the operator's intent is satisfied either way.
  discardCodefixProposal: async (proposalID: string): Promise<void> => {
    const res = await fetch(
      `/api/codefix-proposals/${encodeURIComponent(proposalID)}/discard`,
      { method: "POST", credentials: "include" },
    );
    if (!res.ok && res.status !== 409) {
      throw new Error(`discardCodefixProposal failed: ${res.status} ${res.statusText}`);
    }
  },

  // pushWikiPR creates a PR in the upstream wiki repo for an
  // already-committed wiki entry. Triggered explicitly from the wiki
  // entry detail page. All fields in req are optional.
  pushWikiPR: async (
    slug: string,
    req: WikiPushPRRequest,
  ): Promise<WikiPushPRResponse | WikiPushPRErrorResponse> => {
    return fetchJSON<WikiPushPRResponse | WikiPushPRErrorResponse>(
      `/api/wiki/entries/${encodeURIComponent(slug)}/push-pr`,
      {
        method: "POST",
        body: JSON.stringify(req),
      },
    );
  },

  // resolveWikiEntry sets status=resolved on the wiki entry in the local
  // vault and commits. Idempotent: already-resolved returns ok.
  resolveWikiEntry: async (
    slug: string,
  ): Promise<{ ok: true; commit: string; already_resolved?: boolean }> => {
    return fetchJSON<{ ok: true; commit: string; already_resolved?: boolean }>(
      `/api/wiki/entries/${encodeURIComponent(slug)}/resolve`,
      { method: "POST" },
    );
  },

  // deleteWikiEntry removes the entry file from the local vault and
  // commits the deletion.
  deleteWikiEntry: async (
    slug: string,
  ): Promise<{ ok: true; commit: string }> => {
    return fetchJSON<{ ok: true; commit: string }>(
      `/api/wiki/entries/${encodeURIComponent(slug)}`,
      { method: "DELETE" },
    );
  },

  // deleteWikiEntryPR opens an upstream PR that removes the entry file,
  // then deletes the file locally. The single-call atomicity is
  // load-bearing: once the local file is gone the launcher can't push
  // the deletion separately, so it has to happen here. Used when the
  // entry exists upstream (frontmatter detail's exists_upstream=true).
  deleteWikiEntryPR: async (
    slug: string,
  ): Promise<WikiPushPRResponse | WikiPushPRErrorResponse> => {
    return fetchJSON<WikiPushPRResponse | WikiPushPRErrorResponse>(
      `/api/wiki/entries/${encodeURIComponent(slug)}/delete-pr`,
      { method: "POST", body: JSON.stringify({}) },
    );
  },

  // ── Sessions upstream repo ───────────────────────────────────────────
  // sessionsUpstreamStatus returns metadata about the launcher's clone
  // of the upstream sessions repo (repo, dir, last-synced commit, etc.).
  sessionsUpstreamStatus: () =>
    fetchJSON<SessionsUpstreamStatus>("/api/sessions-upstream"),

  // sessionsUpstreamSync pulls the latest commits from the upstream
  // sessions repo and returns the new HEAD commit + lastSynced timestamp.
  sessionsUpstreamSync: () =>
    fetchJSON<{ ok: true; commit: string; lastSynced: string }>(
      "/api/sessions-upstream/sync",
      { method: "POST" },
    ),

  // refreshPRStates re-pulls `gh pr list` against the upstream
  // sessions repo and reconciles each known PushURL with the result.
  // Returns the count of investigations whose state changed. Failure
  // is non-fatal (gh not authed, offline, no sessions repo); callers
  // can ignore errors and rely on the existing cached state.
  refreshPRStates: () =>
    fetchJSON<{ ok: true; changed: number }>(
      "/api/sessions-upstream/refresh-pr-states",
      { method: "POST" },
    ),

  // listUpstreamSessions returns the index of sessions available in the
  // upstream clone (title, date, author, sources, etc.).
  listUpstreamSessions: () =>
    fetchJSON<{ sessions: SessionCard[] }>("/api/sessions-upstream/sessions").then(
      (r) => r.sessions,
    ),

  // getUpstreamSession fetches the full doc (frontmatter + body) for a
  // single upstream session by slug.
  getUpstreamSession: (slug: string) =>
    fetchJSON<SessionDoc>(
      `/api/sessions-upstream/sessions/${encodeURIComponent(slug)}`,
    ),

  // getUpstreamSessionBundle downloads the raw share bundle for a
  // single upstream session by slug.
  getUpstreamSessionBundle: (slug: string) =>
    fetchJSON<unknown>(
      `/api/sessions-upstream/sessions/${encodeURIComponent(slug)}/bundle`,
    ),

  // archiveInvestigation marks an investigation as archived and returns
  // the updated snapshot.
  archiveInvestigation: (id: string) =>
    fetchJSON<Investigation>(
      `/api/investigations/${encodeURIComponent(id)}/archive`,
      { method: "POST" },
    ),

  // setInvestigationLabel sets the label for an investigation and returns
  // the updated snapshot.
  setInvestigationLabel: (id: string, label: string) =>
    fetchJSON<Investigation>(
      `/api/investigations/${encodeURIComponent(id)}/label`,
      {
        method: "POST",
        body: JSON.stringify({ label }),
      },
    ),

  // pushSessionPR kicks off the upstream push for a completed
  // investigation. The handler now returns 202 Accepted; the actual
  // success/error result arrives on the per-investigation SSE stream
  // as a push_state envelope.
  pushSessionPR: async (
    id: string,
    req: SessionPushPRRequest,
  ): Promise<{ ok: true } | { ok: false; errors: string[] }> => {
    const res = await fetch(
      `/api/investigations/${encodeURIComponent(id)}/push-pr`,
      {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(req),
      },
    );
    if (res.ok) {
      // 202 Accepted — server kicked off the goroutine. Result via SSE.
      return { ok: true };
    }
    if (res.status === 400) {
      try {
        const err = await res.json();
        if (Array.isArray(err?.errors))
          return { ok: false, errors: err.errors as string[] };
      } catch {
        /* fall through */
      }
    }
    let msg = `${res.status} ${res.statusText}`;
    try {
      const b = await res.json();
      if (b?.error) msg = b.error;
    } catch {
      /* keep */
    }
    throw new ApiError(res.status, msg);
  },
};

// buildTagOverrideQS serialises a TagOverrides record into a query
// string (with leading `?`) suitable for appending to the related /
// related-wiki endpoints. Returns "" when no entity has any value,
// so the backend falls through to the playbook's saved tags. Sent
// as repeated `?services=…&services=…` params (Go's r.URL.Query()
// returns them as []string for each key).
function buildTagOverrideQS(overrides?: TagOverrides): string {
  if (!overrides) return "";
  const p = new URLSearchParams();
  for (const s of overrides.services ?? []) p.append("services", s);
  for (const e of overrides.errors ?? []) p.append("errors", e);
  for (const sy of overrides.symptoms ?? []) p.append("symptoms", sy);
  const qs = p.toString();
  return qs ? `?${qs}` : "";
}

// ── Auto-mode helpers ───────────────────────────────────────────────
// Top-level exports (rather than methods on `api`) so callers can
// `import { takeover, resumeAuto } from "@/lib/api"` directly — these
// surfaces are wired only from the SessionView/useAutoMode hook and
// don't need the rest of the `api` object's gravity.
//
// Endpoints are defined in T11 (handlers_auto_browser.go). The launcher
// returns 200 on success and 4xx/5xx with an `error` body on failure.

// takeover hands the operator the wheel on an active auto-mode
// session. On success the launcher pauses the AutoOperator loop and
// emits an `auto_mode_state` envelope (phase=takenOver). The browser
// keeps the cookie-based credential so the launcher can authorise.
export async function takeover(investigationID: string): Promise<void> {
  const res = await fetch(
    `/api/investigations/${encodeURIComponent(investigationID)}/auto/takeover`,
    { method: "POST", credentials: "include" },
  );
  if (!res.ok) {
    let msg = `takeover failed: ${res.status} ${res.statusText}`;
    try {
      const body = await res.json();
      if (body?.error) msg = body.error;
    } catch {
      /* keep status text */
    }
    throw new ApiError(res.status, msg);
  }
}

// resumeAuto reactivates the AutoOperator on a session the operator
// previously took over (or that the operator finished and now wants
// to restart). The launcher reuses the existing OperatorSessionID so
// the resumed loop sees prior history.
export async function resumeAuto(investigationID: string): Promise<void> {
  const res = await fetch(
    `/api/investigations/${encodeURIComponent(investigationID)}/auto/resume`,
    { method: "POST", credentials: "include" },
  );
  if (!res.ok) {
    let msg = `resume failed: ${res.status} ${res.statusText}`;
    try {
      const body = await res.json();
      if (body?.error) msg = body.error;
    } catch {
      /* keep status text */
    }
    throw new ApiError(res.status, msg);
  }
}

// restartAuto is an alias for resumeAuto — the launcher reuses
// OperatorSessionID, so "restart" semantically equals "resume".
// Separate name purely for UI legibility ("Restart auto" reads better
// than "Resume auto" on a finished-then-rerun button).
export const restartAuto = resumeAuto;

// EditorSubject is the discriminated union the frontend uses to address
// editor sessions: a playbook (id + version) or a wiki entry/entity
// (kind + id).
//
// `isNew` + `yaml` are only set for the "+ new playbook" flow, where the
// playbook hasn't been saved to disk yet. The backend falls back to the
// inline YAML in that case so the agent has the operator's draft (e.g. a
// pre-filled symptom) as context instead of failing the lookup.
export type EditorSubject =
  | {
      kind: "playbook";
      playbook: {
        id: string;
        version: string;
        isNew?: boolean;
        yaml?: string;
        type?: string;
      };
    }
  | { kind: "wiki"; wiki: { kind: "entry" | "entity"; id: string } };

// EditorSources are auxiliary upstream feeds attached to a session. The
// backend wires a triagent-slack and/or triagent-incidentio MCP server when the
// corresponding tokens are connected and the source values are non-empty.
// investigationID points at a local investigation whose transcript +
// bundle the agent can mine when drafting a wiki entry; Phase E wires
// the backend side.
export type EditorSources = {
  investigationID?: string;
  incidentioURL?: string;
  slackChannelID?: string;
  slackChannelName?: string;
  slackSinceUnix?: number;
};

// EditorSession is the JSON shape returned by /api/editor-sessions
// endpoints. Mirrors editor.DTO server-side. createdAt is the RFC3339
// string the server marshals; convert at the call site if you need a
// Date.
export type EditorSession = {
  id: string;
  subjectKind: "playbook" | "wiki";
  subjectKey: string;
  sessionDir: string;
  createdAt: string;
  started: boolean;
  streaming: boolean;
  hasInvestigation?: boolean;
  hasSlack?: boolean;
  hasIncidentio?: boolean;
  playbook?: { id: string; version: string; type?: string };
  wiki?: { kind: "entry" | "entity"; id: string };
};

// subjectKey returns the same string the backend computes from
// editor.Subject.Key() — used by findEditorSession's query string.
export function subjectKey(subject: EditorSubject): string {
  return subject.kind === "playbook"
    ? `playbook:${subject.playbook.id}@${subject.playbook.version}`
    : `wiki:${subject.wiki.kind}:${subject.wiki.id}`;
}

// ── Signal Watches ────────────────────────────────────────────────────────────

export type WatchSourceKind = "github_issues" | "slack_channel";

export interface WatchFilter {
  field: string;
  op: "contains" | "does_not_contain" | "regex_matches" | "not_regex_matches";
  value: string;
}

export interface WatchSourceConfig {
  kind: WatchSourceKind;
  owner?: string;
  repo?: string;
  labels?: string[];
  states?: string[];
  channelID?: string;
  channelName?: string;
  includeThreadReplies?: boolean;
  filters?: WatchFilter[];
}

export interface WatchPollingConfig {
  intervalSeconds: number;
}

export interface WatchIngestConfig {
  enabled?: boolean;
  customInstructions?: string;
}

export interface WatchAutoStartConfig {
  enabled: boolean;
  maxConcurrent?: number;
}

export interface Watch {
  id: string;
  name: string;
  description?: string;
  source: WatchSourceConfig;
  polling: WatchPollingConfig;
  ingest?: WatchIngestConfig;
  autoStart?: WatchAutoStartConfig;
  createdAt: string;
  enabled: boolean;
  // Runtime flag (not in YAML). True while the ingestion-agent's
  // claude process is in flight for this watch.
  ingesting?: boolean;
}

export interface SignalRecord {
  id: string;
  watchID: string;
  createdAt: string;
  citedItemIDs: string[];
  outcome: "disabled" | "queued" | "investigation_started" | "proposed" | "unclear" | "dismissed" | "failed";
  investigationID?: string;
  manuallyStarted?: boolean;
  clusters?: string[];
  briefing?: string;
  reason?: string;
  dismissedRelatedTo?: string[];
  dismissedWikiSlugs?: string[];
  errorMessage?: string;
}

export interface WatchIngestRunListEntry {
  id: string;
  startedAt: string;
  durationMs: number;
  exitCode: number;
  itemCount: number;
  status?: "running" | "ok" | "error" | "skipped" | string;
  error?: string;
  userPrompt?: string;
  systemHint?: string;
}

export interface WatchIngestRunDetail extends WatchIngestRunListEntry {
  log: string;
}

export interface ItemRecord {
  id: string;
  watchID: string;
  sourceKind: WatchSourceKind;
  capturedAt: string;
  sourceRef: { owner?: string; repo?: string; issueNumber?: number; channelID?: string; ts?: string };
  snapshot: { title?: string; body?: string; text?: string; author?: string; labels?: string[]; url?: string; permalink?: string };
  signalID?: string;
  filtered?: { ruleIndex: number; summary: string };
}

export const watchesAPI = {
  list: () => fetchJSON<{ watches: Watch[] }>("/api/watches"),

  create: (w: Omit<Watch, "id" | "createdAt">) =>
    fetchJSON<Watch>("/api/watches", {
      method: "POST",
      body: JSON.stringify(w),
    }),

  patch: (id: string, p: Partial<Watch>) =>
    fetchJSON<Watch>(`/api/watches/${encodeURIComponent(id)}`, {
      method: "PATCH",
      body: JSON.stringify(p),
    }),

  remove: async (id: string) => {
    const res = await fetch(`/api/watches/${encodeURIComponent(id)}`, {
      method: "DELETE",
      credentials: "include",
    });
    if (!res.ok && res.status !== 204) {
      let msg = `${res.status} ${res.statusText}`;
      try {
        const body = await res.json();
        if (body?.error) msg = body.error;
      } catch {
        /* keep status */
      }
      throw new ApiError(res.status, msg);
    }
  },

  pollNow: (id: string) =>
    fetchJSON<{ itemsCaptured: number; signalsCreated: number; durationMs: number }>(
      `/api/watches/${encodeURIComponent(id)}/poll-now`,
      { method: "POST" },
    ),

  items: (
    id: string,
    opts: { limit?: number; before?: string; includeFiltered?: boolean } = {},
  ) => {
    const p = new URLSearchParams();
    if (opts.limit) p.set("limit", String(opts.limit));
    if (opts.before) p.set("before", opts.before);
    if (opts.includeFiltered) p.set("includeFiltered", "true");
    const qs = p.toString();
    return fetchJSON<{ items: ItemRecord[] }>(
      `/api/watches/${encodeURIComponent(id)}/items${qs ? `?${qs}` : ""}`,
    );
  },

  signals: (id: string, opts: { limit?: number; before?: string } = {}) => {
    const p = new URLSearchParams();
    if (opts.limit) p.set("limit", String(opts.limit));
    if (opts.before) p.set("before", opts.before);
    const qs = p.toString();
    return fetchJSON<{ signals: SignalRecord[] }>(
      `/api/watches/${encodeURIComponent(id)}/signals${qs ? `?${qs}` : ""}`,
    );
  },

  // Ingestion-agent run audit log. Per-poll claude invocations write
  // metadata + raw output to <watch-dir>/ingest-runs/ — these endpoints
  // surface that so operators can debug why a poll produced no signals.
  ingestRuns: (id: string) =>
    fetchJSON<{ runs: WatchIngestRunListEntry[] }>(
      `/api/watches/${encodeURIComponent(id)}/ingest-runs`,
    ),
  ingestRun: (id: string, runID: string) =>
    fetchJSON<WatchIngestRunDetail>(
      `/api/watches/${encodeURIComponent(id)}/ingest-runs/${encodeURIComponent(runID)}`,
    ),

  clear: (
    id: string,
    body: { items?: boolean; signals?: boolean; olderThan?: string },
  ) =>
    fetchJSON<{ itemsRemoved: number; signalsRemoved: number }>(
      `/api/watches/${encodeURIComponent(id)}/clear`,
      { method: "POST", body: JSON.stringify(body) },
    ),

  deleteLogs: async (id: string) => {
    const res = await fetch(`/api/watches/${encodeURIComponent(id)}/logs`, {
      method: "DELETE",
      credentials: "include",
    });
    if (!res.ok && res.status !== 204) {
      let msg = `${res.status} ${res.statusText}`;
      try {
        const body = await res.json();
        if (body?.error) msg = body.error;
      } catch {
        /* keep status */
      }
      throw new ApiError(res.status, msg);
    }
  },
};
