"use client";

import { useEffect, useState } from "react";
import { usePathname, useRouter } from "next/navigation";
import { api, ApiError, type SessionDoc as SessionDocData } from "@/lib/api";
import { Markdown } from "@/components/shared/Markdown";
import { Spinner } from "@/components/shared/Spinner";

export function SessionDoc() {
  const pathname = usePathname() ?? "";
  const router = useRouter();
  // /sessions/<slug>/ → "<slug>"
  const slug = decodeURIComponent(
    pathname.replace(/^\/sessions\//, "").replace(/\/$/, ""),
  );

  const [doc, setDoc] = useState<SessionDocData | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [opening, setOpening] = useState(false);

  useEffect(() => {
    if (!slug || slug === "_") return;
    api
      .getUpstreamSession(slug)
      .then(setDoc)
      .catch((e) => setError(e instanceof ApiError ? e.message : String(e)));
  }, [slug]);

  // openTranscript routes the operator to a real session view for this
  // upstream session — the live SessionView/SessionWorkspace pair, not
  // a raw bundle dump. Two paths:
  //   1. The upstream session has a local counterpart (matched by
  //      slug). Navigate to the existing /investigations/<id> page so
  //      we don't create a duplicate import.
  //   2. No local counterpart (e.g. someone else pushed it; we synced
  //      down). Fetch the bundle, upload it through the existing
  //      import endpoint, navigate to the imported investigation.
  async function openTranscript() {
    if (opening) return;
    setOpening(true);
    setError(null);
    try {
      const locals = await api.listInvestigations();
      const match = locals.find((inv) => inv.slug === slug);
      if (match) {
        router.push(`/investigations/?id=${encodeURIComponent(match.id)}`);
        return;
      }
      const bundle = await api.getUpstreamSessionBundle(slug);
      const blob = new Blob([JSON.stringify(bundle)], {
        type: "application/json",
      });
      const inv = await api.importInvestigation(blob);
      router.push(`/investigations/?id=${encodeURIComponent(inv.id)}`);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setOpening(false);
    }
  }

  if (error) {
    return (
      <main className="flex-1 overflow-y-auto p-6">
        <div className="rounded border border-red-900/60 bg-red-950/40 p-3 text-sm text-red-200/90">
          {error}
        </div>
      </main>
    );
  }
  if (!doc) {
    return (
      <main className="flex-1 overflow-y-auto p-6">
        <Spinner /> loading…
      </main>
    );
  }
  return (
    <main className="flex-1 overflow-y-auto">
      <div className="mx-auto max-w-3xl space-y-4 px-6 py-6">
        <header className="space-y-2 border-b border-zinc-800 pb-3">
          {/* Title row: heading on the left, replay action on the
              right. Mounted up here (rather than below the markdown)
              so operators on long sessions don't have to scroll past
              everything to find the "see the raw transcript"
              affordance. The button delegates to openTranscript which
              navigates to the matching local session if there is one,
              or imports the bundle and navigates to that. */}
          <div className="flex items-start justify-between gap-3">
            <h1 className="text-xl font-semibold text-zinc-100">
              {doc.frontmatter.title}
            </h1>
            <button
              type="button"
              onClick={openTranscript}
              disabled={opening}
              className="shrink-0 rounded border border-zinc-700 px-3 py-1.5 text-xs text-zinc-200 transition hover:border-zinc-500 disabled:opacity-50"
            >
              {opening ? "opening…" : "replay full transcript"}
            </button>
          </div>
          <div className="flex flex-wrap gap-3 text-xs text-zinc-400">
            <span>{doc.frontmatter.author.name}</span>
            <span>·</span>
            <span className="font-mono">{doc.frontmatter.namespace}</span>
            <span>·</span>
            <time dateTime={doc.frontmatter.date}>{doc.frontmatter.date}</time>
            {doc.frontmatter.sources.incident_io && (
              <a
                href={doc.frontmatter.sources.incident_io}
                target="_blank"
                rel="noreferrer"
                className="text-sky-400"
              >
                incident.io ↗
              </a>
            )}
          </div>
        </header>
        <Markdown text={doc.body} />
      </div>
    </main>
  );
}
