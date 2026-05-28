"use client";

import Link from "next/link";
import type { WikiEntryHit } from "@/lib/wiki-api";
import { UnsyncedIcon } from "@/components/Icons";

type Props = {
  hit: WikiEntryHit;
};

export function EntryRow({ hit }: Props) {
  const fm = hit.frontmatter;
  return (
    <Link
      data-testid="triagent-wiki-entry-row"
      data-entry-id={hit.id}
      href={`/wiki/entries/?slug=${encodeURIComponent(hit.id)}`}
      className="group block rounded-md border border-zinc-800 bg-zinc-900/40 px-4 py-3 transition hover:border-zinc-700 hover:bg-zinc-900"
    >
      <div className="flex flex-wrap items-baseline gap-2">
        {hit.syncState.status !== "synced" && (
          // Cloud-with-up-arrow rather than a warning icon — "unsynced"
          // isn't an error, just "your local copy diverges from
          // origin / push pending". Skipping ONLY the synced case
          // (rather than checking for local-drift specifically) so the
          // icon also shows for any future status the classifier
          // might grow (local-only, closed, unknown). Tooltip text is
          // the resolver's reason — same single source of truth as
          // the playbook / session badges, so wording can't drift
          // between views.
          <span
            title={
              hit.syncState.reason
                ? `${hit.syncState.reason} — push as PR via the entry detail view to share.`
                : "Unsynced local changes — push as PR via the entry detail view to share."
            }
            className="inline-flex shrink-0 cursor-help text-zinc-500"
          >
            <UnsyncedIcon className="h-5 w-5" />
          </span>
        )}
        <span className="shrink-0 font-mono text-xs text-zinc-500">{hit.id}</span>
        <span className="flex-1 truncate text-sm font-medium text-zinc-100 group-hover:text-white">
          {fm.title || hit.title}
        </span>
        <div className="flex shrink-0 items-center gap-2">
          {fm.severity && (
            <SeverityBadge severity={fm.severity} />
          )}
          {fm.status && (
            <StatusBadge status={fm.status} />
          )}
        </div>
      </div>
      <div className="mt-1 flex items-center gap-3">
        <time className="text-xs text-zinc-500" dateTime={fm.date}>
          {fm.date}
        </time>
        {fm.services && fm.services.length > 0 && (
          <span className="truncate text-xs text-zinc-600">
            {fm.services.slice(0, 3).join(", ")}
            {fm.services.length > 3 && " +more"}
          </span>
        )}
      </div>
      {hit.snippet && (
        <p className="mt-1.5 truncate text-xs text-zinc-500">{hit.snippet}</p>
      )}
    </Link>
  );
}

function SeverityBadge({ severity }: { severity: string }) {
  const cls =
    severity === "sev1"
      ? "bg-red-900/60 text-red-300 border-red-800"
      : severity === "sev2"
        ? "bg-amber-900/50 text-amber-300 border-amber-800"
        : "bg-sky-900/50 text-sky-300 border-sky-800";
  return (
    <span className={`rounded border px-1.5 py-0.5 text-xs font-medium ${cls}`}>
      {severity}
    </span>
  );
}

function StatusBadge({ status }: { status: string }) {
  const cls =
    status === "resolved"
      ? "bg-emerald-900/50 text-emerald-300 border-emerald-800"
      : status === "open"
        ? "bg-zinc-800 text-zinc-300 border-zinc-700"
        : "bg-zinc-800/60 text-zinc-500 border-zinc-700";
  return (
    <span className={`rounded border px-1.5 py-0.5 text-xs ${cls}`}>
      {status}
    </span>
  );
}
