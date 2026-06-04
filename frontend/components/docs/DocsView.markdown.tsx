"use client";

import { useEffect, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { slugify, stringifyChildren } from "./DocsView.utils";

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
export function Mermaid({ chart }: { chart: string }) {
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
export function DocsMarkdown({ text }: { text: string }) {
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
