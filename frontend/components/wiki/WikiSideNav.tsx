"use client";

import { useEffect, useState } from "react";
import { wikiApi, ApiError } from "@/lib/wiki-api";
import type { WikiEntityRef } from "@/lib/wiki-api";
import { EntityBrowser } from "./EntityBrowser";

// WikiSideNav renders a right-rail with the entity browser.
// Only mounted on the wiki home page; detail pages keep just the
// leftmost Sidebar so the reader has more horizontal room for the
// post-mortem / entity body.

type Props = {
  className?: string;
};

export function WikiSideNav({ className = "" }: Props) {
  const [entities, setEntities] = useState<WikiEntityRef[]>([]);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    let cancelled = false;
    wikiApi
      .listEntities()
      .then((ents) => {
        if (cancelled) return;
        setEntities(ents.entities);
        setReady(true);
      })
      .catch((err) => {
        if (cancelled) return;
        // Silently swallow 503 (wiki not configured) — the main page
        // handles the "not configured" state, the sidenav just shows nothing.
        if (err instanceof ApiError && err.status === 503) {
          setReady(true);
          return;
        }
        // Other errors: still mark ready so the sidebar doesn't block render.
        setReady(true);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <aside
      className={`flex flex-col gap-6 overflow-y-auto border-l border-zinc-800 px-4 py-6 ${className}`}
    >
      <section className="space-y-2">
        <h2 className="text-xs font-semibold uppercase tracking-wide text-zinc-500">
          Entities
        </h2>
        {!ready && <p className="text-xs text-zinc-600">loading…</p>}
        {ready && entities.length === 0 && (
          <p className="text-xs text-zinc-600">No entities yet.</p>
        )}
        {entities.length > 0 && (
          <EntityBrowser entities={entities} compact />
        )}
      </section>
    </aside>
  );
}
