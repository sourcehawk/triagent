"use client";

import Link from "next/link";
import type { SessionCard as SessionCardData, SyncStatePR } from "@/lib/api";
import { relativeTime } from "@/lib/relative-time";
import { ExternalLinkIcon } from "@/components/shared/Icons";

export function SessionCard({ card }: { card: SessionCardData }) {
  // PR badge data flows from the per-card SyncState.pr sub-object —
  // the launcher's resolver has already joined this slug with the
  // matching local Investigation (or marked it upstream-only). No
  // client-side slug→PR map: the wire format is the answer.
  const pr = card.syncState.pr;
  return (
    <Link
      href={`/sessions/${encodeURIComponent(card.slug)}`}
      className="block rounded border border-zinc-800 bg-zinc-900/30 p-4 transition hover:border-zinc-700 hover:bg-zinc-900/60"
    >
      <div className="flex items-baseline justify-between gap-3">
        <h3 className="text-sm font-medium text-zinc-100">{card.title}</h3>
        <div className="flex shrink-0 items-center gap-2">
          {pr && <PRBadge pr={pr} />}
          <time className="text-xs text-zinc-500" dateTime={card.date}>
            {card.date}
          </time>
        </div>
      </div>
      <div className="mt-2 flex flex-wrap items-center gap-3 text-xs text-zinc-400">
        {card.namespace && (
          <>
            <span className="font-mono">{card.namespace}</span>
            <span>·</span>
          </>
        )}
        <span>{card.author.name}</span>
        {card.sources.incident_io && (
          <>
            <span>·</span>
            <a
              href={card.sources.incident_io}
              target="_blank"
              rel="noreferrer"
              className="text-sky-400 hover:text-sky-300"
              onClick={(e) => e.stopPropagation()}
            >
              incident.io ↗
            </a>
          </>
        )}
      </div>
    </Link>
  );
}

// PRBadge renders a state-coloured pill linking to the PR. State is
// always present on the wire (server's SyncStatePR.state); UI uses it
// to pick the colour family + label.
function PRBadge({ pr }: { pr: SyncStatePR }) {
  const visual = badgeTone(pr.state);
  const when =
    pr.state === "merged" && pr.mergedAt
      ? `merged ${relativeTime(pr.mergedAt)}`
      : pr.state === "closed" && pr.closedAt
        ? `closed ${relativeTime(pr.closedAt)}`
        : "";
  const title = (when ? `${visual.title} (${when})` : visual.title) + ` — ${pr.url}`;
  return (
    <a
      href={pr.url}
      target="_blank"
      rel="noreferrer"
      onClick={(e) => e.stopPropagation()}
      className={
        "inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[10px] uppercase tracking-wide transition " +
        visual.classes
      }
      title={title}
    >
      {visual.label}
      <ExternalLinkIcon className="h-3 w-3 opacity-70" />
    </a>
  );
}

function badgeTone(state?: "open" | "merged" | "closed") {
  switch (state) {
    case "merged":
      return {
        label: "merged",
        title: "PR merged",
        classes:
          "border-violet-900/60 bg-violet-950/30 text-violet-300 hover:border-violet-700 hover:text-violet-200",
      };
    case "closed":
      return {
        label: "closed",
        title: "PR closed without merging",
        classes:
          "border-zinc-700 bg-zinc-900/40 text-zinc-400 hover:border-zinc-500 hover:text-zinc-200",
      };
    case "open":
    default:
      return {
        label: "PR open",
        title: "PR open — review pending",
        classes:
          "border-emerald-900/60 bg-emerald-950/30 text-emerald-300 hover:border-emerald-700 hover:text-emerald-200",
      };
  }
}
