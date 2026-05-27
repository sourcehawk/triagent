"use client";

import { useEffect, useRef, useState } from "react";

// Mermaid renders a fenced ```mermaid``` block as an SVG diagram.
// Mermaid is large (~700KB after minification) so we dynamic-import
// it inside the effect rather than at module load — the diagram is
// only paid for when a page actually contains one.
//
// One mermaid.initialize() call per process; subsequent renders reuse
// the running config. Theme is dark to match the rest of the docs site;
// securityLevel: "loose" is required for some diagram types (sequence,
// flowchart with html labels) to render at all under our static export.
export function Mermaid({ chart }: { chart: string }) {
  const [svg, setSvg] = useState<string>("");
  const [err, setErr] = useState<string | null>(null);
  // Stable id per instance so concurrent diagrams don't fight over the
  // same DOM target inside mermaid.render's hidden scratch node.
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
    // SVG comes from mermaid.render against a static chart string we
    // ship in the repo — not user-supplied content. Wrapper centres
    // the diagram and lets it scroll horizontally on narrow viewports.
    <div
      className="my-4 overflow-x-auto rounded border border-zinc-800 bg-zinc-900/30 p-3 [&_svg]:mx-auto"
      // eslint-disable-next-line react/no-danger
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  );
}
