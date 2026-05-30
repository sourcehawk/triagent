"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

// Section ids match the markdown filenames under /public/docs/. The
// human label drives the left-rail rendering; the slug is the URL
// query value the page persists (so deep links into /?view=docs&docs=mcp
// land on the right page).
type SectionID = "overview" | "investigations" | "watches" | "mcp" | "playbooks" | "wiki" | "repos" | "connections" | "cloud-providers" | "profiles";

const SECTIONS: { id: SectionID; label: string; subtitle: string }[] = [
  {
    id: "overview",
    label: "Overview",
    subtitle: "What Triagent is and what you can do with it",
  },
  {
    id: "investigations",
    label: "Investigate",
    subtitle: "AI-driven cluster triage",
  },
  {
    id: "watches",
    label: "Watches",
    subtitle: "Persistent eyes-on a source",
  },
  {
    id: "mcp",
    label: "MCP",
    subtitle: "Tool servers the agent uses",
  },
  {
    id: "playbooks",
    label: "Playbooks",
    subtitle: "Structured procedural knowledge",
  },
  {
    id: "repos",
    label: "Repos",
    subtitle: "Linked GitHub projects + architecture summaries",
  },
  {
    id: "wiki",
    label: "Wiki",
    subtitle: "Persistent know-how",
  },
  {
    id: "connections",
    label: "Connections",
    subtitle: "Slack and incident.io integrations",
  },
  {
    id: "cloud-providers",
    label: "Cloud providers",
    subtitle: "Read-only GCP and AWS investigation context",
  },
  {
    id: "profiles",
    label: "Profiles",
    subtitle: "Forking the default to fit your platform",
  },
];

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

// SectionGroup is one row of the docs nav: the section header
// (label + subtitle, clickable to switch sections) with an inline
// nested outline of H2/H3 headings rendered beneath when this
// section is the active one. Non-active sections collapse to just
// the header — only one outline is ever visible at a time.
function SectionGroup({
  label,
  subtitle,
  active,
  expanded,
  onClick,
  outline,
  activeSlug,
}: {
  label: string;
  subtitle: string;
  // True when this section is the page's currently-selected one. Drives
  // the row's highlighted background regardless of outline collapse
  // state — collapsing the outline shouldn't visually demote the row.
  active: boolean;
  // True when this section's outline should render. Always implies
  // active; the gap between the two flags is what makes the active
  // section collapsible without losing its "you are here" highlight.
  expanded: boolean;
  onClick: () => void;
  outline: Heading[];
  // Slug of the heading currently in the operator's viewport. Drives
  // the highlighted-row state on the matching outline entry. Null
  // when this section isn't active or no heading is in view yet.
  activeSlug: string | null;
}) {
  return (
    <div className="flex flex-col">
      <button
        type="button"
        onClick={onClick}
        aria-pressed={active}
        aria-expanded={expanded}
        className={
          "rounded px-2 py-2 text-left transition " +
          (active
            ? "bg-zinc-800 text-zinc-100"
            : "text-zinc-300 hover:bg-zinc-900 hover:text-zinc-100")
        }
      >
        <div className="flex items-center gap-1.5">
          <ChevronIcon
            className={
              "h-3 w-3 shrink-0 text-zinc-500 transition-transform " +
              (expanded ? "rotate-90" : "")
            }
          />
          <div className="text-sm font-medium">{label}</div>
        </div>
        <div className="pl-[18px] text-xs text-zinc-500">{subtitle}</div>
      </button>
      {expanded && outline.length > 0 && (
        // Nested heading outline. Indented to align with the section
        // label, with a left rule so the parent-child relationship
        // reads at a glance even when the operator scrolls past
        // the section header.
        <ul className="ml-3 mt-1 flex flex-col gap-0.5 border-l border-zinc-800 py-1 pl-1">
          {outline.map((h) => {
            const isActive = h.slug === activeSlug;
            const indent = h.level === 3 ? "pl-5" : "";
            // Active row gets a stronger text colour + a subtle pill
            // so the operator can see at a glance which subsection
            // the main pane is currently on. Hover styles still apply
            // so non-active rows visibly respond to the cursor.
            const tone = isActive
              ? "bg-zinc-800/80 text-zinc-100"
              : h.level === 3
                ? "text-zinc-500 hover:bg-zinc-900 hover:text-zinc-200"
                : "text-zinc-300 hover:bg-zinc-900 hover:text-zinc-200";
            return (
              <li key={h.slug}>
                <a
                  href={`#${h.slug}`}
                  aria-current={isActive ? "location" : undefined}
                  className={`block truncate rounded px-2 py-1 text-xs transition ${indent} ${tone}`}
                >
                  {h.text}
                </a>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

// ChevronIcon is the rotate-on-open caret used for the section
// headers. Inline SVG (not the unicode glyph) so it picks up
// currentColor on the zinc UI without OS-emoji-font hijack.
function ChevronIcon({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 12 12"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden
    >
      <path d="M4.5 3l3 3-3 3" />
    </svg>
  );
}

// Mermaid renders a fenced ```mermaid``` block as an SVG diagram.
// Mermaid is large (~700KB after minification) so we dynamic-import
// it inside the effect — the docs view is opt-in already, and this
// keeps the rest of the launcher's bundle tight for operators who
// never touch the docs.
//
// One mermaid.initialize() call per process; subsequent renders
// reuse the running config. Theme is dark to match the rest of the
// UI; securityLevel: "loose" is required for some diagram types
// (sequence, flowchart with html labels) to render at all under
// our embed-as-static-export deployment.
function Mermaid({ chart }: { chart: string }) {
  const [svg, setSvg] = useState<string>("");
  const [err, setErr] = useState<string | null>(null);
  // Stable id per instance so concurrent diagrams don't fight over
  // the same DOM target inside mermaid.render's hidden scratch node.
  const idRef = useRef<string>(
    "mermaid-" + Math.random().toString(36).slice(2, 10),
  );

  useEffect(() => {
    let cancelled = false;
    setErr(null);
    setSvg("");
    (async () => {
      try {
        const mermaid = (await import("mermaid")).default;
        mermaid.initialize({
          startOnLoad: false,
          theme: "dark",
          securityLevel: "loose",
          // Slightly bigger default font so labels read at the
          // pane width the docs use (max-w-3xl ≈ 768px). Mermaid's
          // default 14px reads thin against zinc backgrounds.
          fontSize: 14,
        });
        const { svg } = await mermaid.render(idRef.current, chart);
        if (cancelled) return;
        setSvg(svg);
      } catch (e) {
        if (cancelled) return;
        setErr(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [chart]);

  if (err) {
    return (
      <pre className="overflow-x-auto rounded border border-amber-900/60 bg-amber-950/30 p-3 text-xs text-amber-200">
        Could not render diagram: {err}
        {"\n\n"}
        {chart}
      </pre>
    );
  }
  if (!svg) {
    return (
      <div className="rounded border border-zinc-800 bg-zinc-900/30 p-3 text-xs text-zinc-500">
        rendering diagram…
      </div>
    );
  }
  return (
    // The SVG comes from mermaid.render, not user-supplied content
    // — the chart string is from a static .md file we ship. Wrapper
    // styles centre the diagram and let it scroll horizontally on
    // narrow viewports (some flowcharts are wider than the prose
    // column).
    <div
      className="my-4 overflow-x-auto rounded border border-zinc-800 bg-zinc-900/30 p-3 [&_svg]:mx-auto"
      // eslint-disable-next-line react/no-danger
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  );
}

// DocsMarkdown wraps ReactMarkdown with documentation-flavoured
// styling — bigger spacing than the chat Markdown component, real
// heading sizes, anchor-friendly id'd headings.
//
// Heading components stamp an id derived from the text content so the
// outline's `#slug` anchors actually scroll to them. Slug logic must
// match extractOutline exactly so the two surfaces stay in sync.
function DocsMarkdown({ text }: { text: string }) {
  return (
    <div
      className={
        "prose prose-invert max-w-none " +
        "prose-headings:font-semibold prose-headings:tracking-tight " +
        "prose-h1:text-2xl prose-h1:mt-0 prose-h1:mb-4 " +
        "prose-h2:text-xl prose-h2:mt-8 prose-h2:mb-3 prose-h2:border-b prose-h2:border-zinc-800 prose-h2:pb-1 " +
        "prose-h3:text-base prose-h3:mt-5 prose-h3:mb-2 " +
        "prose-p:my-3 prose-p:leading-relaxed prose-p:text-zinc-300 " +
        "prose-li:text-zinc-300 prose-li:my-1 " +
        "prose-strong:text-zinc-100 prose-strong:font-semibold " +
        "prose-code:before:content-[''] prose-code:after:content-[''] prose-code:bg-zinc-800 prose-code:rounded prose-code:px-1 prose-code:py-0.5 prose-code:font-mono prose-code:text-[0.85em] prose-code:text-zinc-200 " +
        "prose-pre:bg-zinc-950 prose-pre:border prose-pre:border-zinc-800 prose-pre:rounded prose-pre:p-3 " +
        "prose-pre:prose-code:bg-transparent prose-pre:prose-code:p-0 prose-pre:prose-code:text-zinc-200 " +
        "prose-a:text-sky-300 prose-a:no-underline hover:prose-a:underline " +
        "prose-table:text-sm prose-th:text-zinc-200 prose-td:text-zinc-300 " +
        "prose-blockquote:border-l-zinc-700 prose-blockquote:text-zinc-400"
      }
    >
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          h1: ({ children }) => (
            <h1 id={slugify(stringifyChildren(children))}>{children}</h1>
          ),
          h2: ({ children }) => (
            <h2 id={slugify(stringifyChildren(children))}>{children}</h2>
          ),
          h3: ({ children }) => (
            <h3 id={slugify(stringifyChildren(children))}>{children}</h3>
          ),
          // Intercept fenced ```mermaid``` blocks and render them
          // through the mermaid library instead of as a code block.
          // Anything else (json/yaml/bash/...) falls through to
          // ReactMarkdown's default code rendering.
          code: (props) => {
            const { className, children } = props as {
              className?: string;
              children?: React.ReactNode;
            };
            const lang = (className ?? "").replace(/^language-/, "");
            if (lang === "mermaid") {
              return <Mermaid chart={String(children).replace(/\n$/, "")} />;
            }
            return <code className={className}>{children}</code>;
          },
          // Block code opts out of prose typography so the inline-code
          // pill styling (bg-zinc-800 / rounded / px-1) doesn't leak
          // onto the inner <code> and fragment its background across
          // line wraps. Styled directly here.
          pre: ({ children }) => (
            <pre className="not-prose my-4 overflow-x-auto rounded border border-zinc-800 bg-zinc-950 p-3 font-mono text-[0.85em] leading-relaxed text-zinc-200">
              {children}
            </pre>
          ),
        }}
      >
        {text}
      </ReactMarkdown>
    </div>
  );
}

type Heading = { level: 2 | 3; text: string; slug: string };

// extractOutline pulls H2 + H3 headings from raw markdown. Skips H1
// (always the page title — the outline doesn't need to repeat it) and
// H4+ (too noisy for a sidebar). Code-block-aware: lines inside ```
// fences don't count, so a YAML example with `## something` doesn't
// pollute the outline.
function extractOutline(md: string): Heading[] {
  const out: Heading[] = [];
  let inFence = false;
  for (const raw of md.split("\n")) {
    const line = raw;
    if (line.startsWith("```")) {
      inFence = !inFence;
      continue;
    }
    if (inFence) continue;
    const m = /^(#{2,3})\s+(.+?)\s*$/.exec(line);
    if (!m) continue;
    const level = m[1].length === 2 ? 2 : 3;
    const text = stripInlineMarkdown(m[2].trim());
    out.push({ level, text, slug: slugify(text) });
  }
  return out;
}

// stripInlineMarkdown unwraps `[text](url)` to `text` and strips
// surrounding `*`/`_` emphasis and backticks so heading labels render
// as plain prose in the sidebar outline. Mirrors what react-markdown
// does to the heading body — the outline-side label and the rendered
// heading should read identically. Only handles the constructs we
// actually use in heading lines; not a full markdown parser.
function stripInlineMarkdown(s: string): string {
  return s
    .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
    .replace(/`([^`]+)`/g, "$1")
    .replace(/\*\*([^*]+)\*\*/g, "$1")
    .replace(/\*([^*]+)\*/g, "$1")
    .replace(/_([^_]+)_/g, "$1");
}

// slugify makes a URL-safe anchor target. Same algorithm both
// outline-side and heading-component-side; if you change one, change
// the other or the anchors break silently.
function slugify(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^\w\s-]/g, "")
    .trim()
    .replace(/\s+/g, "-")
    .slice(0, 60);
}

// stringifyChildren extracts a flat string from React markdown
// children — react-markdown passes a mix of strings + elements (e.g.
// inline code spans inside a heading). We only need the visible text
// for slug derivation.
function stringifyChildren(children: React.ReactNode): string {
  if (children === null || children === undefined) return "";
  if (typeof children === "string") return children;
  if (typeof children === "number") return String(children);
  if (Array.isArray(children)) return children.map(stringifyChildren).join("");
  if (typeof children === "object" && "props" in children) {
    return stringifyChildren(
      (children as { props: { children?: React.ReactNode } }).props.children,
    );
  }
  return "";
}
