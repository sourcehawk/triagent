"use client";

import { type Heading } from "./DocsView.utils";

// SectionGroup is one row of the docs nav: the section header
// (label + subtitle, clickable to switch sections) with an inline
// nested outline of H2/H3 headings rendered beneath when this
// section is the active one. Non-active sections collapse to just
// the header — only one outline is ever visible at a time.
export function SectionGroup({
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
export function ChevronIcon({ className }: { className?: string }) {
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
