"use client";

import Link from "next/link";
import { useEffect, useMemo, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { SECTIONS, type SectionID } from "@/lib/sections";
import { Mermaid } from "./Mermaid";

// DocsView mirrors the launcher's docs surface so the published site
// reads as the same product:
//   - Left rail: section list with the active section's H2/H3 outline
//     expanded inline; clicking a non-active section navigates to its
//     own pre-rendered page.
//   - Top bar: project name + a "view on GitHub" link, since the
//     GitHub Pages deploy is the public face of the project.
//   - Main pane: markdown rendered with the same prose styling as the
//     in-launcher docs view, with `id`-stamped headings so the
//     outline's anchor links land on them.
//
// The launcher's DocsView fetches markdown at runtime; here we accept
// it as a prop so each section ships as fully pre-rendered HTML — no
// flash-of-loading, deep-links render server-side, and search engines
// see the body content.

type Props = {
  active: SectionID;
  markdown: string;
};

// next/Link auto-prepends basePath, and Next.js's static export
// rewrites internal <a href="/..."> client-side too. So URLs we hand
// to <Link> or rewriteUrl stay as plain absolute paths — never include
// basePath here, or it gets doubled.
const GITHUB_URL = "https://github.com/sourcehawk/triagent";

// Official GitHub Octicon (16px mark, MIT). Inlined rather than imported
// from the launcher frontend so docs/site stays a self-contained package.
function GitHubIcon({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 16 16"
      fill="currentColor"
      className={className}
      aria-hidden
    >
      <path
        fillRule="evenodd"
        d="M8 0C3.58 0 0 3.58 0 8a8 8 0 0 0 5.47 7.59c.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0 0 16 8c0-4.42-3.58-8-8-8z"
      />
    </svg>
  );
}

export function DocsView({ active, markdown }: Props) {
  const [activeSlug, setActiveSlug] = useState<string | null>(null);
  const [collapsed, setCollapsed] = useState(false);
  const scrollRef = useRef<HTMLDivElement | null>(null);

  // Outline is derived from the markdown body — same regex pass the
  // launcher uses, so the slug algorithm and the heading-id stamper
  // stay in lock-step.
  const outline = useMemo(() => extractOutline(markdown), [markdown]);

  // Reset outline highlight, collapse state, and scroll position on
  // section switch. The [section]/page.tsx file is a separate route
  // per section so React remounts DocsView on each one — useState
  // reseeds naturally — but explicit reset keeps the behaviour
  // identical in dev (fast refresh can preserve state) and means the
  // new active section opens expanded even if the operator collapsed
  // the previous one.
  useEffect(() => {
    setActiveSlug(null);
    setCollapsed(false);
    if (scrollRef.current) scrollRef.current.scrollTop = 0;
  }, [active]);

  // Track which heading is currently in the top band of the viewport
  // and highlight it in the outline. Re-arms whenever the markdown
  // body changes (i.e. on section switch).
  useEffect(() => {
    const scrollEl = scrollRef.current;
    if (!scrollEl) return;
    const headings = Array.from(
      scrollEl.querySelectorAll<HTMLElement>("h2[id], h3[id]"),
    );
    if (headings.length === 0) return;

    // Seed: until the operator scrolls, the first heading is active so
    // the outline isn't blank on initial render.
    setActiveSlug(headings[0].id);

    const visible = new Set<string>();
    const observer = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          const slug = (e.target as HTMLElement).id;
          if (e.isIntersecting) visible.add(slug);
          else visible.delete(slug);
        }
        // headings is in document order — the first one currently
        // visible inside the top band wins.
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
        // Only count a heading as "active" while it sits in the top
        // ~25% of the viewport. Without the negative bottom margin
        // every visible heading would intersect and the highlight
        // would jitter on scroll.
        rootMargin: "0px 0px -75% 0px",
        threshold: 0,
      },
    );
    for (const h of headings) observer.observe(h);
    return () => observer.disconnect();
  }, [markdown]);

  return (
    <div className="flex h-screen flex-col">
      <TopBar />
      <div className="flex min-h-0 flex-1">
        {/* Left rail mirrors the launcher's section list. Each row is
            a Next.js Link so clicking pre-fetches the destination
            section's static HTML. The active row expands its inline
            heading outline beneath. */}
        <aside className="flex w-72 shrink-0 flex-col gap-1 overflow-y-auto border-r border-zinc-800 bg-zinc-950 px-3 py-4">
          <nav className="flex flex-col gap-1">
            {SECTIONS.map((s) => {
              const isActive = s.id === active;
              const href = sectionHref(s.id);
              return (
                <SectionGroup
                  key={s.id}
                  href={href}
                  label={s.label}
                  subtitle={s.subtitle}
                  active={isActive}
                  outline={isActive ? outline : []}
                  activeSlug={isActive ? activeSlug : null}
                  collapsed={isActive && collapsed}
                  onToggleCollapse={() => setCollapsed((c) => !c)}
                />
              );
            })}
          </nav>
        </aside>

        <main
          ref={scrollRef}
          className="min-w-0 flex-1 overflow-y-auto px-8 py-8"
        >
          <div className="mx-auto max-w-3xl">
            <DocsMarkdown text={markdown} />
          </div>
        </main>
      </div>
    </div>
  );
}

// TopBar is the thin docs-site chrome: project mark, current section
// breadcrumb, and a "view on GitHub" link. The launcher has its own
// chrome around DocsView, so this surface only exists on the public
// site.
function TopBar() {
  return (
    <header className="flex shrink-0 items-center justify-between border-b border-zinc-800 bg-zinc-950 px-6 py-3">
      <Link
        href="/"
        className="flex items-center gap-2 text-sm font-semibold tracking-tight text-zinc-100 hover:text-white"
      >
        <span className="rounded bg-zinc-100 px-1.5 py-0.5 font-mono text-xs text-zinc-950">
          ▲
        </span>
        Triagent
        <span className="text-xs font-normal text-zinc-500">Docs</span>
      </Link>
      <a
        href={GITHUB_URL}
        target="_blank"
        rel="noreferrer noopener"
        aria-label="View on GitHub"
        className="text-zinc-400 transition hover:text-zinc-100"
      >
        <GitHubIcon className="h-5 w-5" />
      </a>
    </header>
  );
}

// SectionGroup is one row of the docs nav: the section header (label
// + subtitle, links to the dedicated route) with the inline outline
// of H2/H3 headings rendered beneath when this is the active section
// and the operator hasn't collapsed it. The chevron on the active row
// is a toggle button overlaid on the link's reserved chevron slot;
// non-active rows keep a decorative chevron so the row stays a single
// large click target for navigation.
function SectionGroup({
  href,
  label,
  subtitle,
  active,
  outline,
  activeSlug,
  collapsed,
  onToggleCollapse,
}: {
  href: string;
  label: string;
  subtitle: string;
  active: boolean;
  outline: Heading[];
  activeSlug: string | null;
  collapsed: boolean;
  onToggleCollapse: () => void;
}) {
  const showOutline = active && !collapsed;
  return (
    <div className="relative flex flex-col">
      <Link
        href={href}
        aria-current={active ? "page" : undefined}
        className={
          "rounded px-2 py-2 text-left transition " +
          (active
            ? "bg-zinc-800 text-zinc-100"
            : "text-zinc-300 hover:bg-zinc-900 hover:text-zinc-100")
        }
      >
        <div className="flex items-center gap-1.5">
          {/* Spacer reserves the chevron's slot so the label and the
              subtitle's pl-[18px] stay aligned. The chevron itself is
              rendered as an overlay below — separate so the active
              row's toggle button can intercept clicks without
              swallowing the row's navigation. */}
          <span className="h-3 w-3 shrink-0" aria-hidden />
          <div className="text-sm font-medium">{label}</div>
        </div>
        <div className="pl-[18px] text-xs text-zinc-500">{subtitle}</div>
      </Link>
      {active ? (
        <button
          type="button"
          onClick={onToggleCollapse}
          aria-expanded={!collapsed}
          aria-label={
            collapsed ? `Show ${label} outline` : `Hide ${label} outline`
          }
          className="absolute left-1 top-2 flex h-5 w-5 items-center justify-center rounded text-zinc-500 transition hover:bg-zinc-700 hover:text-zinc-200"
        >
          <ChevronIcon
            className={
              "h-3 w-3 transition-transform " +
              (showOutline ? "rotate-90" : "")
            }
          />
        </button>
      ) : (
        <span
          aria-hidden
          className="pointer-events-none absolute left-1 top-2 flex h-5 w-5 items-center justify-center text-zinc-500"
        >
          <ChevronIcon className="h-3 w-3" />
        </span>
      )}
      {showOutline && outline.length > 0 && (
        <ul className="ml-3 mt-1 flex flex-col gap-0.5 border-l border-zinc-800 py-1 pl-1">
          {outline.map((h) => {
            const isActive = h.slug === activeSlug;
            const indent = h.level === 3 ? "pl-5" : "";
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

// DocsMarkdown wraps ReactMarkdown with the same documentation prose
// styling used by the launcher's DocsView, plus a URL transform that
// rewrites markdown-side links written for the launcher to work on
// the public docs site.
function DocsMarkdown({ text }: { text: string }) {
  return (
    <div
      className={
        "prose prose-invert max-w-none " +
        "prose-headings:font-semibold prose-headings:tracking-tight " +
        "prose-h1:text-3xl prose-h1:mt-0 prose-h1:mb-4 " +
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
        urlTransform={rewriteUrl}
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
          // ``` mermaid blocks render as SVG diagrams via the mermaid
          // library; other fenced blocks fall through to the default
          // <code> rendering.
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
          // Block code (<pre>) opts out of prose typography via
          // not-prose so the inline-code pill styling (bg-zinc-800 /
          // rounded / px-1) doesn't leak onto the inner <code> and
          // fragment its background across line wraps. Styled directly
          // instead of via prose-pre:* variants.
          pre: ({ children }) => (
            <pre className="not-prose my-4 overflow-x-auto rounded border border-zinc-800 bg-zinc-950 p-3 font-mono text-[0.85em] leading-relaxed text-zinc-200">
              {children}
            </pre>
          ),
          // Render internal (absolute-path) links as Next.js <Link>
          // so basePath prepending + client-side prefetching kick in;
          // anchor-only and external links use plain <a>. Without this
          // GH Pages would serve the docs at /triagent/ but markdown-
          // side links like "/repos/" would 404.
          a: ({ href, children, ...rest }) => {
            const target = href ?? "";
            const isInternal =
              target.startsWith("/") && !target.startsWith("//");
            if (isInternal) {
              return (
                <Link href={target} {...rest}>
                  {children}
                </Link>
              );
            }
            return (
              <a href={target} {...rest}>
                {children}
              </a>
            );
          },
        }}
      >
        {text}
      </ReactMarkdown>
    </div>
  );
}

// rewriteUrl maps the markdown-side links (authored against the
// launcher's URL space) to the docs-site URL space.
//
//   /docs/<section>           -> /<section>/        (this site)
//   /docs/<section>#anchor    -> /<section>/#anchor (this site)
//   /docs                     -> /                  (this site)
//   /mcp, /investigations,... -> the launcher repo  (launcher-only
//                                                    routes; send the
//                                                    operator to
//                                                    GitHub rather
//                                                    than a 404)
//
// External (http/https) and intra-page anchor (#…) links pass through
// unchanged. Returned paths are plain absolute paths (no basePath)
// since Next.js's Link auto-prepends basePath at render time.
function rewriteUrl(url: string): string {
  if (!url) return url;
  if (/^[a-z]+:/i.test(url)) return url; // http:, https:, mailto:, …
  if (url.startsWith("#")) return url;
  const m = url.match(/^\/docs(?:\/([^#?]+))?([#?].*)?$/);
  if (m) {
    const section = m[1] ? m[1].replace(/\/$/, "") : "";
    const tail = m[2] ?? "";
    const path = section ? `/${section}/` : "/";
    return `${path}${tail}`;
  }
  if (url.startsWith("/")) {
    return GITHUB_URL;
  }
  return url;
}

function sectionHref(id: SectionID): string {
  return `/${id === "overview" ? "" : `${id}/`}`;
}

type Heading = { level: 2 | 3; text: string; slug: string };

// extractOutline pulls H2 + H3 headings from raw markdown. Skips H1
// (page title — the outline doesn't repeat it) and H4+ (too noisy).
// Code-block-aware: lines inside ``` fences are ignored so a YAML
// example with `## something` can't pollute the outline.
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

function stripInlineMarkdown(s: string): string {
  return s
    .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
    .replace(/`([^`]+)`/g, "$1")
    .replace(/\*\*([^*]+)\*\*/g, "$1")
    .replace(/\*([^*]+)\*/g, "$1")
    .replace(/_([^_]+)_/g, "$1");
}

// slugify must stay identical to the algorithm the heading-id stamper
// uses; if you change one, change the other or the anchor links break.
function slugify(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^\w\s-]/g, "")
    .trim()
    .replace(/\s+/g, "-")
    .slice(0, 60);
}

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
