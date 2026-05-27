// Typed API client for the wiki browse endpoints.
// Mirrors the style of lib/api.ts (fetchJSON + the api object pattern).

import { ApiError, type SyncState } from "@/lib/api";
export { ApiError };

// ── Types ────────────────────────────────────────────────────────────────────

export type WikiLinks = {
  investigation?: string;
  incident_io?: string;
  slack_channel?: string;
  slack_message?: string;
};

export type WikiFrontmatter = {
  schema_version?: number;
  // id is the slug used as the wiki entry's filename + URL segment.
  // Free-form lowercase-with-hyphens (`^[a-z0-9][a-z0-9-]*$`).
  id: string;
  date: string;
  title: string;
  status: string;
  severity?: string;
  services: string[];
  errors: string[];
  symptoms: string[];
  links: WikiLinks;
};

export type WikiEntityFrontmatter = {
  schema_version?: number;
  type: string;
  name: string;
  created: string;
};

export type WikiEntryHit = {
  id: string;
  path: string;
  title: string;
  snippet?: string;
  frontmatter: WikiFrontmatter;
  score: number;
  // syncState — resolver's drift answer, mirrored from the launcher
  // (internal/server/sync_state.go). status === "local-drift" means
  // the file's local commits aren't on origin/HEAD yet; the
  // cloud-with-up-arrow icon renders for that case. The icon stays
  // hidden for "synced". Tooltip uses the resolver's reason text so
  // list / detail / editor agree on the wording.
  syncState: SyncState;
};

export type WikiEntryListResponse = {
  hits: WikiEntryHit[];
  total: number;
  offset: number;
  limit: number;
};

export type WikiEntryDetail = {
  slug: string;
  path: string;
  frontmatter: WikiFrontmatter;
  markdown: string;
  syncState: SyncState;
  // exists_upstream is true when the wiki entry file is present on
  // origin/HEAD (or origin/main as a fallback). Drives the delete
  // confirmation copy: present-upstream → "open a PR upstream",
  // absent → "delete from local storage".
  exists_upstream?: boolean;
  // is_stub is true when the file does not exist on disk yet and the
  // backend synthesised an empty entry so the editor can mount. The
  // wiki editor uses this to auto-open the chat drawer — the operator
  // is here to draft, not to read an existing entry.
  is_stub?: boolean;
};

export type WikiEntityRef = {
  name: string;
  type: string;
  path: string;
  entry_count: number;
};

export type WikiEntityListResponse = {
  entities: WikiEntityRef[];
};

export type WikiBacklink = {
  id: string;
  path: string;
  title: string;
  date: string;
  severity: string;
  status: string;
};

export type WikiEntityDetail = {
  name: string;
  type: string;
  path: string;
  frontmatter: WikiEntityFrontmatter;
  markdown: string;
  backlinks: WikiBacklink[];
  // syncState — same resolver answer as wiki entries. Drives the
  // cloud-with-up-arrow indicator on the entity node when the
  // status is "local-drift".
  syncState: SyncState;
  // is_stub is true when the file does not exist on disk yet. Same
  // semantics as WikiEntryDetail.is_stub — drives auto-open chat in
  // the wiki editor.
  is_stub?: boolean;
};

// WikiBundleFile describes a single file in a push-pr-bundle request.
// Either slug is set (kind=entry) or entity_type+entity_name
// are set (kind=entity).
export type WikiBundleFile =
  | { kind: "entry"; slug: string }
  | { kind: "entity"; entity_type: string; entity_name: string };

export type WikiPushBundleResult = {
  ok: boolean;
  url?: string;
  branch?: string;
  base?: string;
  errors?: string[];
};

// WikiOpenPR is a single open upstream PR matched by branch-name to a
// wiki page's root display id. Used by the push modal to surface
// existing in-flight work for the same root.
export type WikiOpenPR = {
  number: number;
  url: string;
  title: string;
  branch: string;
  updated_at?: string;
};

export type WikiOpenPRsResponse = {
  prs: WikiOpenPR[];
  // skipped is set when the lookup was a no-op (gh not authenticated,
  // gh failed). The modal displays nothing in that case rather than
  // bouncing the user — listing existing PRs is informational, not
  // load-bearing.
  skipped?: string;
};

export type WikiStats = {
  entry_count: number;
  entity_count_by_type: {
    service: number;
    error: number;
    symptom: number;
    component: number;
  };
  last_promoted_at: string;
};

// WikiUpstreamStatus mirrors the JSON returned by GET /api/wiki-upstream.
// Lets the wiki home header render "synced from <repo> · last synced N ago"
// and decide whether the Sync button is actionable.
export type WikiUpstreamStatus = {
  repo: string;
  dir: string;
  gitCheckout: boolean;
  commit?: string;
  lastSynced?: string;
  remoteAhead: number;
  remoteAheadError?: string;
  error?: string;
};

// WikiProposalListItem mirrors handlers_wiki.go::wikiProposalListItem. The
// sidenav reads this to surface pending wiki proposals; clicking a row
// opens the editor and auto-switches to the AI proposal tab.
export type WikiProposalListItem = {
  proposal_id: string;
  slug: string;
  title?: string;
  status?: string;
  severity?: string;
  is_new: boolean;
  modified_at: string;
};

export type WikiProposalListResponse = {
  proposals: WikiProposalListItem[];
};

export type WikiEntryFilters = {
  q?: string;
  services?: string;
  errors?: string;
  symptoms?: string;
  severity?: string;
  status?: string;
  // unsynced narrows the listing to entries whose local file diverges
  // from origin/HEAD (or isn't there yet) — matches the cloud-up icon
  // shown on each entry row.
  unsynced?: boolean;
  limit?: number;
  offset?: number;
};

// ── fetchJSON helper (mirrors lib/api.ts) ────────────────────────────────────

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

// ── API surface ───────────────────────────────────────────────────────────────

export const wikiApi = {
  getStats: () => fetchJSON<WikiStats>("/api/wiki/stats"),

  listEntries: (filters: WikiEntryFilters = {}) => {
    const params = new URLSearchParams();
    if (filters.q) params.set("q", filters.q);
    if (filters.services) params.set("services", filters.services);
    if (filters.errors) params.set("errors", filters.errors);
    if (filters.symptoms) params.set("symptoms", filters.symptoms);
    if (filters.severity) params.set("severity", filters.severity);
    if (filters.status) params.set("status", filters.status);
    if (filters.unsynced) params.set("unsynced", "true");
    if (filters.limit != null) params.set("limit", String(filters.limit));
    if (filters.offset != null) params.set("offset", String(filters.offset));
    const qs = params.toString();
    return fetchJSON<WikiEntryListResponse>(
      `/api/wiki/entries${qs ? "?" + qs : ""}`,
    );
  },

  getEntry: (slug: string) =>
    fetchJSON<WikiEntryDetail>(
      `/api/wiki/entries/${encodeURIComponent(slug)}`,
    ),

  listEntities: (type?: string) => {
    const qs = type ? `?type=${encodeURIComponent(type)}` : "";
    return fetchJSON<WikiEntityListResponse>(`/api/wiki/entities${qs}`);
  },

  getEntity: (type: string, name: string) =>
    fetchJSON<WikiEntityDetail>(
      `/api/wiki/entities/${encodeURIComponent(type)}/${encodeURIComponent(name)}`,
    ),

  // listProposals enumerates pending wiki proposals on disk so the
  // sidenav can surface them on both /wiki and /wiki/entries.
  listProposals: () =>
    fetchJSON<WikiProposalListResponse>("/api/wiki-proposals"),

  // listCorrelatedSessions returns the set of investigation_session ids
  // that appear in any wiki entry's frontmatter. The wiki home pane
  // subtracts this from the local investigations list to surface
  // sessions that don't yet have a wiki entry.
  listCorrelatedSessions: () =>
    fetchJSON<{ session_ids: string[] }>("/api/wiki/correlated-sessions"),

  getUpstreamStatus: () => fetchJSON<WikiUpstreamStatus>("/api/wiki-upstream"),

  syncUpstream: () =>
    fetchJSON<{ ok: true; commit: string; lastSynced: string }>(
      "/api/wiki-upstream/sync",
      { method: "POST" },
    ),

  // updateEntry PATCHes title/severity/markdown on an existing
  // wiki entry file. Frontmatter fields not in the request are preserved
  // verbatim. Returns {ok,commit} on success or {ok:false,errors:[]}
  // on per-field validation failures (title empty, body missing
  // required headers, severity invalid).
  updateEntry: (
    slug: string,
    patch: { title?: string; severity?: string | null; markdown: string },
  ) =>
    fetchJSON<{ ok: boolean; commit?: string; noop?: boolean; errors?: string[] }>(
      `/api/wiki/entries/${encodeURIComponent(slug)}`,
      { method: "PUT", body: JSON.stringify(patch) },
    ),

  // updateEntity replaces only the body of an entity file. Frontmatter
  // (type/name/created) is preserved verbatim — renames break backlinks
  // so the operator can't reach them through this endpoint.
  updateEntity: (
    type: string,
    name: string,
    patch: { markdown: string },
  ) =>
    fetchJSON<{ ok: boolean; commit?: string; noop?: boolean; errors?: string[] }>(
      `/api/wiki/entities/${encodeURIComponent(type)}/${encodeURIComponent(name)}`,
      { method: "PUT", body: JSON.stringify(patch) },
    ),

  // pushPRBundle opens a single upstream PR that bundles edits to a
  // root file plus zero or more linked files. The launcher derives
  // sensible default title/branch when those fields are empty.
  // listOpenPRs returns open upstream PRs whose head branch matches the
  // given root display id (e.g. a wiki entry slug or `entity_type/name`).
  // Used by the push modal to warn before opening a duplicate PR.
  listOpenPRs: (rootDisplay: string) =>
    fetchJSON<WikiOpenPRsResponse>(
      `/api/wiki/open-prs?root=${encodeURIComponent(rootDisplay)}`,
    ),

  pushPRBundle: (req: {
    files: WikiBundleFile[];
    root_display?: string;
    title?: string;
    body?: string;
    branch?: string;
    base?: string;
  }) =>
    fetchJSON<WikiPushBundleResult>(
      "/api/wiki/push-pr-bundle",
      { method: "POST", body: JSON.stringify(req) },
    ),
};
