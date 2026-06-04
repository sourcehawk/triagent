"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { SECTIONS, type SectionID } from "@/lib/docs-sections";
import { SectionGroup } from "./DocsView.nav";
import { DocsMarkdown } from "./DocsView.markdown";
import { extractOutline } from "./DocsView.utils";

type Props = {
  // Section the operator picked from the docs sidebar. Driven by the
  // page-level URL state so deep links work.
  active: SectionID;
  onSectionChange: (next: SectionID) => void;
};

// DocsView is the top-level docs page. Two-rail layout:
//   - Left rail: the four top-level sections (investigations, mcp,
//     playbooks, wiki) plus an auto-generated outline of the
//     selected section's H2/H3 headings.
//   - Main pane: the markdown for the selected section, fetched from
//     /docs/<id>.md (a static asset bundled into the launcher's
//     embedded frontend).
//
// Markdown source-of-truth lives at repo-root docs/content/<id>.md.
// scripts/sync-docs.mjs mirrors them into frontend/public/docs/ at
// build time so the static export serves them at /docs/<id>.md. The
// separate GitHub Pages docs site under docs/site/ consumes the same
// markdown directly.
export function DocsView({ active, onSectionChange }: Props) {
  const [markdown, setMarkdown] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  // The slug of the heading currently in the top-of-viewport band.
  // Drives the active-row highlight in the sidenav outline. Reset on
  // section switch so the highlight matches the new content.
  const [activeSlug, setActiveSlug] = useState<string | null>(null);
  // Whether the active section's inline outline is manually collapsed.
  // Clicking the already-active section header toggles this; switching
  // to a different section auto-resets it to false (a fresh section
  // always opens expanded).
  const [outlineCollapsed, setOutlineCollapsed] = useState(false);
  // Force-reset the in-pane scroll position on section switch so the
  // operator never lands halfway down a long doc by mistake.
  const scrollRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    let cancelled = false;
    setMarkdown(null);
    setErr(null);
    setActiveSlug(null);
    setOutlineCollapsed(false);
    fetch(`/docs/${active}.md`)
      .then(async (res) => {
        if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
        return res.text();
      })
      .then((text) => {
        if (cancelled) return;
        setMarkdown(text);
        // Reset scroll on section switch — useEffect runs after the
        // markdown renders, so the scrollRef is populated.
        if (scrollRef.current) scrollRef.current.scrollTop = 0;
      })
      .catch((e) => {
        if (cancelled) return;
        setErr(e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [active]);

  // Outline derived from the markdown's H2 + H3 headings. Re-runs
  // whenever the markdown changes; cheap regex scan, no parser needed
  // since we control the source.
  const outline = useMemo(() => extractOutline(markdown ?? ""), [markdown]);

  // Track which heading the operator is currently looking at and
  // highlight it in the sidenav. Uses an IntersectionObserver scoped
  // to the scroll container, with a tall negative bottom margin so
  // only the heading nearest the top of the viewport counts as
  // "active". Re-arms whenever the markdown changes (new section ->
  // new set of heading nodes).
  useEffect(() => {
    if (markdown === null) return;
    const scrollEl = scrollRef.current;
    if (!scrollEl) return;
    const headings = Array.from(
      scrollEl.querySelectorAll<HTMLElement>("h2[id], h3[id]"),
    );
    if (headings.length === 0) return;

    // Seed: until the user scrolls, treat the first heading as active
    // so the outline isn't blank on initial render.
    setActiveSlug(headings[0].id);

    const visible = new Set<string>();
    const observer = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          const slug = (e.target as HTMLElement).id;
          if (e.isIntersecting) visible.add(slug);
          else visible.delete(slug);
        }
        // Pick the topmost heading currently in the band — headings is
        // in document order, so the first visible id wins.
        let next: string | null = null;
        for (const h of headings) {
          if (visible.has(h.id)) {
            next = h.id;
            break;
          }
        }
        if (next) setActiveSlug(next);
      },
      {
        root: scrollEl,
        // Counts a heading as "active" only while it sits in the top
        // ~25% of the viewport. Without the negative bottom margin
        // every heading on a tall pane would be intersecting and the
        // highlight would jitter.
        rootMargin: "0px 0px -75% 0px",
        threshold: 0,
      },
    );
    for (const h of headings) observer.observe(h);
    return () => observer.disconnect();
  }, [markdown]);

  return (
    <div className="flex h-full">
      {/* Left rail: nested section list. The currently-open section
          expands its heading outline inline beneath its row; the
          others stay collapsed. Picking another section auto-collapses
          the previous one. Clicking the active section header toggles
          its outline (collapsed ↔ expanded) so operators can free up
          vertical space without leaving the page.
          w-72 to fit two-line section blurbs without truncating. */}
      <aside className="flex w-72 shrink-0 flex-col gap-1 overflow-y-auto border-r border-zinc-800 bg-zinc-950 px-3 py-4">
        <nav className="flex flex-col gap-1">
          {SECTIONS.map((s) => {
            const isActive = s.id === active;
            const expanded = isActive && !outlineCollapsed;
            return (
              <SectionGroup
                key={s.id}
                label={s.label}
                subtitle={s.subtitle}
                active={isActive}
                expanded={expanded}
                onClick={() => {
                  if (isActive) {
                    setOutlineCollapsed((c) => !c);
                  } else {
                    onSectionChange(s.id);
                  }
                }}
                outline={expanded ? outline : []}
                activeSlug={expanded ? activeSlug : null}
              />
            );
          })}
        </nav>
      </aside>

      {/* Main pane: scrollable markdown with id'd headings so the
          outline anchors land on them. */}
      <main
        ref={scrollRef}
        className="min-w-0 flex-1 overflow-y-auto px-8 py-8"
      >
        <div className="mx-auto max-w-3xl">
          {err && (
            <div className="rounded border border-red-900/60 bg-red-950/40 p-3 text-sm text-red-200/90">
              Could not load <code>/docs/{active}.md</code>: {err}
            </div>
          )}
          {markdown === null && !err && (
            <div className="text-sm text-zinc-500">loading…</div>
          )}
          {markdown !== null && <DocsMarkdown text={markdown} />}
        </div>
      </main>
    </div>
  );
}
