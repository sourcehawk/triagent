import type React from "react";

export type Heading = { level: 2 | 3; text: string; slug: string };

// extractOutline pulls H2 + H3 headings from raw markdown. Skips H1
// (always the page title — the outline doesn't need to repeat it) and
// H4+ (too noisy for a sidebar). Code-block-aware: lines inside ```
// fences don't count, so a YAML example with `## something` doesn't
// pollute the outline.
export function extractOutline(md: string): Heading[] {
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
export function stripInlineMarkdown(s: string): string {
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
export function slugify(s: string): string {
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
export function stringifyChildren(children: React.ReactNode): string {
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
