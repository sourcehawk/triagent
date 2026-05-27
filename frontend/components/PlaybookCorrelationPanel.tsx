"use client";

import { useEffect, useState } from "react";
import {
  api,
  type RelatedPlaybookMatch,
  type RelatedWikiMatch,
  type TagOverrides,
} from "@/lib/api";

// PLAYBOOK_DRAFT_TAGS_CHANGED is the global event the PlaybookEditor
// dispatches whenever the operator edits a chip in the entity-tag
// inputs. The detail carries the current draft's tag set so the
// correlation panel (which lives in the sidebar, separate React tree)
// can refetch with those overrides and preview correlations live —
// without the operator having to save first.
//
// Keep this string stable; it's the only contract between the two
// components.
export const PLAYBOOK_DRAFT_TAGS_CHANGED =
  "triagent:playbook-draft-tags-changed";

export type PlaybookDraftTagsDetail = {
  playbookID: string;
  services: string[];
  errors: string[];
  symptoms: string[];
};

// PLAYBOOKS_EXPANDED_KEY / WIKI_EXPANDED_KEY persist the operator's
// expand/collapse choice per sub-strip across reloads. Mirrors
// LinkedReposPanel's EXPANDED_KEY pattern.
const PLAYBOOKS_EXPANDED_KEY = "triagent.correlation.playbooks.expanded";
const WIKI_EXPANDED_KEY = "triagent.correlation.wiki.expanded";

function readExpanded(key: string): boolean {
  if (typeof window === "undefined") return false;
  return window.localStorage.getItem(key) === "1";
}

type Props = {
  playbookID: string;
  onSelect: (id: string) => void;
};

// PlaybookCorrelationPanel mounts in the sidebar under the playbook
// "related" tray. Two collapsed-by-default sub-strips: related
// playbooks (intra-vault correlation) and related wiki entries
// (cross-vault — past incidents that touched the same entities).
//
// Both sub-strips re-query whenever the editor dispatches
// PLAYBOOK_DRAFT_TAGS_CHANGED, using the current draft's tag set
// as the query override. Without an override they fall back to the
// playbook's saved tags on the server.
export function PlaybookCorrelationPanel({ playbookID, onSelect }: Props) {
  // Track the editor's in-progress tag draft. null = no override yet
  // (use saved tags); non-null = explicit override (preview mode).
  const [overrides, setOverrides] = useState<TagOverrides | null>(null);

  useEffect(() => {
    // Reset on playbook switch — the previous editor's draft no longer
    // applies to a different id.
    setOverrides(null);
  }, [playbookID]);

  useEffect(() => {
    const handler = (e: Event) => {
      const detail = (e as CustomEvent<PlaybookDraftTagsDetail>).detail;
      if (!detail || detail.playbookID !== playbookID) return;
      setOverrides({
        services: detail.services,
        errors: detail.errors,
        symptoms: detail.symptoms,
      });
    };
    window.addEventListener(PLAYBOOK_DRAFT_TAGS_CHANGED, handler);
    return () => window.removeEventListener(PLAYBOOK_DRAFT_TAGS_CHANGED, handler);
  }, [playbookID]);

  return (
    <div className="space-y-0">
      <CorrelationStrip
        title="related playbooks"
        storageKey={PLAYBOOKS_EXPANDED_KEY}
        help={
          <>
            Other playbooks that share entity tags with this one. Score is{" "}
            <span className="font-medium text-zinc-200">3 × direct hits</span>{" "}
            +{" "}
            <span className="font-medium text-zinc-200">1 × lifted hits</span>{" "}
            (entities tagged on a one-hop child via{" "}
            <span className="font-mono">delegate_to</span> /{" "}
            <span className="font-mono">handoff</span>). Multiple matching
            entities accumulate.
          </>
        }
      >
        <PlaybookList
          playbookID={playbookID}
          overrides={overrides}
          onSelect={onSelect}
        />
      </CorrelationStrip>

      <CorrelationStrip
        title="related wiki entries"
        storageKey={WIKI_EXPANDED_KEY}
        help={
          <>
            Past incidents from the wiki vault that share entity tags with
            this playbook. Score is{" "}
            <span className="font-medium text-zinc-200">3 × direct hits</span>{" "}
            — no lifting since wiki entries don&apos;t delegate / handoff.
            Multiple matching entities accumulate.
          </>
        }
      >
        <WikiList playbookID={playbookID} overrides={overrides} />
      </CorrelationStrip>
    </div>
  );
}

// CorrelationStrip is one collapsible header-strip + body pair. The
// (?) icon next to the title surfaces a hover popover with a brief
// explanation of what the strip's scoring means.
function CorrelationStrip({
  title,
  storageKey,
  help,
  children,
}: {
  title: string;
  storageKey: string;
  help: React.ReactNode;
  children: React.ReactNode;
}) {
  const [expanded, setExpanded] = useState<boolean>(() =>
    readExpanded(storageKey),
  );
  const [helpVisible, setHelpVisible] = useState(false);

  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem(storageKey, expanded ? "1" : "0");
  }, [storageKey, expanded]);

  return (
    <div className="border-t border-zinc-800">
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        aria-expanded={expanded}
        className="flex w-full items-center justify-between gap-2 px-3 py-2.5 text-left transition hover:bg-zinc-900/40"
      >
        <span className="flex items-baseline gap-1.5 text-xs uppercase tracking-wide text-zinc-500">
          <span aria-hidden className="inline-block w-2 text-zinc-600">
            {expanded ? "▾" : "▴"}
          </span>
          {title}
        </span>
        <span
          className="relative inline-flex"
          onMouseEnter={() => setHelpVisible(true)}
          onMouseLeave={() => setHelpVisible(false)}
          onFocus={() => setHelpVisible(true)}
          onBlur={() => setHelpVisible(false)}
        >
          <span
            role="button"
            tabIndex={0}
            aria-label={`what is "${title}"?`}
            onClick={(e) => e.stopPropagation()}
            className="inline-flex h-5 w-5 items-center justify-center rounded-full border border-zinc-700 text-xs text-zinc-400 transition hover:border-zinc-500 hover:text-zinc-200"
          >
            ?
          </span>
          {helpVisible && (
            // Anchor left + push right so the tooltip stays on-screen
            // inside the narrow w-64 sidebar (right-anchored shoves the
            // left edge past the viewport).
            <span
              role="tooltip"
              className="absolute left-full top-0 z-20 ml-2 w-72 rounded-md border border-zinc-700 bg-zinc-950 px-3 py-2.5 text-xs leading-relaxed text-zinc-300 shadow-xl"
              onClick={(e) => e.stopPropagation()}
            >
              {help}
              <span
                aria-hidden
                className="absolute right-full top-2 -mr-px h-0 w-0 border-y-[6px] border-r-[6px] border-y-transparent border-r-zinc-700"
              />
            </span>
          )}
        </span>
      </button>

      {expanded && (
        <div className="max-h-64 overflow-y-auto px-3 pb-3">{children}</div>
      )}
    </div>
  );
}

function PlaybookList({
  playbookID,
  overrides,
  onSelect,
}: {
  playbookID: string;
  overrides: TagOverrides | null;
  onSelect: (id: string) => void;
}) {
  const [matches, setMatches] = useState<RelatedPlaybookMatch[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    setMatches(null);
    setError(null);
    api
      .getRelatedPlaybooks(playbookID, overrides ?? undefined)
      .then((m) => {
        if (alive) setMatches(m);
      })
      .catch((e) => {
        if (alive) setError(String(e));
      });
    return () => {
      alive = false;
    };
  }, [playbookID, overrides]);

  if (error) {
    return (
      <div className="text-xs text-rose-400">Unavailable: {error}</div>
    );
  }
  if (matches === null) {
    return <div className="text-xs text-zinc-500">Loading…</div>;
  }
  if (matches.length === 0) {
    return (
      <div className="text-xs text-zinc-500">
        No related playbooks. Tag this one with services / errors / symptoms
        to surface neighbours.
      </div>
    );
  }
  return (
    <ul className="space-y-1.5">
      {matches.map((m) => (
        <li key={m.id}>
          <button
            type="button"
            className="block w-full text-left text-xs text-sky-400 hover:underline"
            onClick={() => onSelect(m.id)}
          >
            <span className="font-mono">{m.id}</span>
            <span className="ml-1.5 text-zinc-500">score {m.score}</span>
            {m.symptom && (
              <div className="mt-0.5 text-[11px] font-normal leading-snug text-zinc-500">
                {m.symptom}
              </div>
            )}
          </button>
          {m.match_path.lifted && m.match_path.lifted.length > 0 && (
            <div className="ml-2 text-[11px] text-zinc-500">
              via{" "}
              {[...new Set(m.match_path.lifted.map((l) => l.via))].join(", ")}
            </div>
          )}
        </li>
      ))}
    </ul>
  );
}

function WikiList({
  playbookID,
  overrides,
}: {
  playbookID: string;
  overrides: TagOverrides | null;
}) {
  const [matches, setMatches] = useState<RelatedWikiMatch[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    setMatches(null);
    setError(null);
    api
      .getRelatedWikiEntries(playbookID, overrides ?? undefined)
      .then((m) => {
        if (alive) setMatches(m);
      })
      .catch((e) => {
        if (alive) setError(String(e));
      });
    return () => {
      alive = false;
    };
  }, [playbookID, overrides]);

  if (error) {
    return (
      <div className="text-xs text-rose-400">Unavailable: {error}</div>
    );
  }
  if (matches === null) {
    return <div className="text-xs text-zinc-500">Loading…</div>;
  }
  if (matches.length === 0) {
    return (
      <div className="text-xs text-zinc-500">
        No wiki entries match this playbook&apos;s tags yet.
      </div>
    );
  }
  return (
    <ul className="space-y-1.5">
      {matches.map((m) => (
        <li key={m.id}>
          <a
            href={`/wiki/${encodeURIComponent(m.id)}`}
            className="block text-xs text-sky-400 hover:underline"
          >
            <span className="font-mono">{m.id}</span>
            <span className="ml-1.5 text-zinc-500">score {m.score}</span>
            {m.title && (
              <div className="mt-0.5 text-[11px] font-normal leading-snug text-zinc-300">
                {m.title}
              </div>
            )}
            {(m.status || m.severity) && (
              <div className="text-[11px] text-zinc-500">
                {[m.status, m.severity].filter(Boolean).join(" · ")}
              </div>
            )}
          </a>
        </li>
      ))}
    </ul>
  );
}
