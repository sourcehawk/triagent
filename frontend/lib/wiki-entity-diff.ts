// Wiki entity extraction + diffing for proposal preview.
//
// A wiki incident page references entities in two places:
//   1. Frontmatter arrays — `services:`, `errors:`, `symptoms:`. The
//      array a name appears in determines its entity type.
//   2. Body wikilinks — `[[entity-name]]` literal references. A
//      body-only mention (not present in any frontmatter array)
//      defaults to type "component" — mirrors the fallback in the Go
//      `createEntityStubs` path (mcp/internal/wiki/tool_propose.go).
//
// Compared to the server's stub-creation logic this is a read-only
// view: we don't look up existing entity files in the vault, we just
// classify what the markdown itself declares. That's enough for a
// diff between two markdown blobs.

import yaml from "js-yaml";

export type WikiEntityType = "service" | "error" | "symptom" | "component";

export type WikiEntitySource = "frontmatter" | "body";

export type WikiEntity = {
  name: string;
  type: WikiEntityType;
  // Where this entity is mentioned. An entity present in both the
  // frontmatter array AND a body `[[wikilink]]` carries both sources.
  sources: WikiEntitySource[];
};

export type WikiEntityStatus =
  | "added"
  | "removed"
  | "modified"
  | "unchanged";

export type WikiEntityDiffEntry = {
  name: string;
  status: WikiEntityStatus;
  // For "removed" this is the *base* entity (so the renderer can show
  // its prior type/sources as a ghost). For everything else it's the
  // proposed entity.
  entity: WikiEntity;
  // Only populated when status === "modified". Names the dimensions
  // that flipped between base and proposed.
  changes: ("type" | "sources")[];
  // Previous type when changes includes "type" — drives tooltips.
  previousType?: WikiEntityType;
  // Previous sources when changes includes "sources".
  previousSources?: WikiEntitySource[];
};

export type WikiEntityDiff = {
  // Keyed by entity name (canonical lowercase, hyphenated).
  entities: Map<string, WikiEntityDiffEntry>;
};

// Matches the server's wikilink pattern in
// mcp/internal/wiki/tool_propose.go (`wikilinkPattern`). Names are
// lowercase + hyphen + digits — anything else is intentionally not a
// wikilink so we don't accidentally treat `[[TODO]]` as an entity.
const WIKILINK_RE = /\[\[([a-z0-9][a-z0-9-]*)\]\]/g;

// splitFrontmatter pulls the leading YAML block off a markdown blob.
// Mirrors the server's frontmatter handling: the file must start with
// `---\n`, and the block closes on the next `\n---` (or `\n---\n`).
function splitFrontmatter(md: string): { fm: string; body: string } {
  if (!md.startsWith("---")) return { fm: "", body: md };
  // Skip the opening fence and find the closing one.
  const second = md.indexOf("\n---", 3);
  if (second < 0) return { fm: "", body: md };
  const fm = md.slice(4, second);
  const body = md.slice(second + 4).replace(/^\s+/, "");
  return { fm, body };
}

type ParsedFrontmatter = {
  services?: unknown;
  errors?: unknown;
  symptoms?: unknown;
};

function asNameArray(v: unknown): string[] {
  if (!Array.isArray(v)) return [];
  const out: string[] = [];
  for (const item of v) {
    if (typeof item === "string" && item.trim().length > 0) {
      out.push(item.trim());
    }
  }
  return out;
}

// parseWikiEntities extracts the entity references declared by a wiki
// markdown blob. Returns a map keyed by entity name; values include
// type + the set of places the name appeared.
export function parseWikiEntities(md: string): Map<string, WikiEntity> {
  const out = new Map<string, WikiEntity>();
  if (!md) return out;

  const { fm, body } = splitFrontmatter(md);

  // 1. Frontmatter classification. If the YAML is malformed we treat
  //    the file as having no frontmatter classification — wikilinks
  //    still get picked up below and default to "component".
  let parsed: ParsedFrontmatter | null = null;
  if (fm) {
    try {
      const raw = yaml.load(fm);
      if (raw && typeof raw === "object") {
        parsed = raw as ParsedFrontmatter;
      }
    } catch {
      parsed = null;
    }
  }

  const classify = (
    names: string[],
    type: WikiEntityType,
  ): void => {
    for (const name of names) {
      const existing = out.get(name);
      if (existing) {
        if (!existing.sources.includes("frontmatter")) {
          existing.sources.push("frontmatter");
        }
        // A name appearing in multiple frontmatter arrays is a
        // schema-level inconsistency; first-wins keeps the diff
        // deterministic.
      } else {
        out.set(name, {
          name,
          type,
          sources: ["frontmatter"],
        });
      }
    }
  };

  if (parsed) {
    classify(asNameArray(parsed.services), "service");
    classify(asNameArray(parsed.errors), "error");
    classify(asNameArray(parsed.symptoms), "symptom");
  }

  // 2. Body wikilinks. Reset the regex's lastIndex defensively in
  //    case the same regex object is reused across calls (we declare
  //    it module-level with /g so it carries state).
  WIKILINK_RE.lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = WIKILINK_RE.exec(body)) !== null) {
    const name = m[1];
    const existing = out.get(name);
    if (existing) {
      if (!existing.sources.includes("body")) {
        existing.sources.push("body");
      }
    } else {
      out.set(name, {
        name,
        type: "component",
        sources: ["body"],
      });
    }
  }

  return out;
}

function sourcesEqual(
  a: WikiEntitySource[],
  b: WikiEntitySource[],
): boolean {
  if (a.length !== b.length) return false;
  // Both arrays are short (≤2) and source order is determined by the
  // parser pass order (frontmatter first, then body) — so direct
  // membership checks suffice.
  for (const s of a) if (!b.includes(s)) return false;
  return true;
}

// diffWikiEntities computes the per-entity change set between two
// wiki markdown blobs. When `base` is omitted (new wiki, no prior
// version) every entity in `proposed` is "added".
export function diffWikiEntities(
  proposedMd: string,
  baseMd?: string,
): WikiEntityDiff {
  const proposed = parseWikiEntities(proposedMd);
  const base =
    baseMd && baseMd.length > 0
      ? parseWikiEntities(baseMd)
      : new Map<string, WikiEntity>();

  const entities = new Map<string, WikiEntityDiffEntry>();
  const allNames = new Set<string>([...base.keys(), ...proposed.keys()]);

  for (const name of allNames) {
    const b = base.get(name);
    const p = proposed.get(name);
    if (!b && p) {
      entities.set(name, { name, status: "added", entity: p, changes: [] });
      continue;
    }
    if (b && !p) {
      entities.set(name, { name, status: "removed", entity: b, changes: [] });
      continue;
    }
    // Both sides present — figure out what changed.
    const changes: ("type" | "sources")[] = [];
    if (b!.type !== p!.type) changes.push("type");
    if (!sourcesEqual(b!.sources, p!.sources)) changes.push("sources");
    if (changes.length === 0) {
      entities.set(name, {
        name,
        status: "unchanged",
        entity: p!,
        changes: [],
      });
    } else {
      entities.set(name, {
        name,
        status: "modified",
        entity: p!,
        changes,
        previousType: changes.includes("type") ? b!.type : undefined,
        previousSources: changes.includes("sources")
          ? b!.sources
          : undefined,
      });
    }
  }

  return { entities };
}

// Convenience: the set of statuses actually present in a diff. Used
// by the graph's legend to dim statuses that don't appear.
export function presentStatuses(
  diff: WikiEntityDiff,
): Set<WikiEntityStatus> {
  const out = new Set<WikiEntityStatus>();
  for (const e of diff.entities.values()) out.add(e.status);
  return out;
}
