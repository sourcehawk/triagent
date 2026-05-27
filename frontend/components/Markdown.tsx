"use client";

import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

// Tight Tailwind prose tuned for the dark transcript pane. We disable a
// few of @tailwindcss/typography's defaults (bigger fonts, oversized
// margins) because the transcript is a chat surface, not a docs page.
const PROSE =
  "prose prose-sm prose-invert max-w-none " +
  "prose-p:my-2 prose-p:leading-relaxed " +
  "prose-headings:mb-2 prose-headings:mt-4 prose-headings:font-semibold " +
  "prose-pre:bg-zinc-950 prose-pre:border prose-pre:border-zinc-800 prose-pre:my-3 prose-pre:p-3 prose-pre:rounded " +
  "prose-code:before:content-[''] prose-code:after:content-[''] prose-code:bg-zinc-800 prose-code:rounded prose-code:px-1 prose-code:py-0.5 prose-code:font-mono prose-code:text-[0.85em] " +
  "prose-pre:prose-code:bg-transparent prose-pre:prose-code:p-0 " +
  "prose-a:text-zinc-300 prose-a:underline hover:prose-a:text-zinc-100 " +
  "prose-li:my-0.5 " +
  "prose-strong:text-zinc-100 prose-strong:font-semibold";

export function Markdown({ text }: { text: string }) {
  return (
    <div className={PROSE}>
      <ReactMarkdown remarkPlugins={[remarkGfm]}>{text}</ReactMarkdown>
    </div>
  );
}
