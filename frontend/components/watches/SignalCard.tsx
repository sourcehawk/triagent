"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import type { ItemRecord, SignalRecord } from "@/lib/api";
import { ArrowRightIcon, ExternalLinkIcon } from "@/components/shared/Icons";
import { StartFromSignalDialog } from "@/components/watches/StartFromSignalDialog";

const outcomeAccent: Record<SignalRecord["outcome"], string> = {
  disabled: "border-l-sky-400",
  queued: "border-l-amber-300",
  investigation_started: "border-l-emerald-500",
  proposed: "border-l-amber-400",
  unclear: "border-l-amber-500",
  dismissed: "border-l-zinc-500",
  failed: "border-l-red-500",
};

export function SignalCard({
  signal,
  itemsByID,
}: {
  signal: SignalRecord;
  // ID-indexed items so the card can render cited content inline. The
  // panel owns the fetch; the card just looks up by id.
  itemsByID?: Map<string, ItemRecord>;
}) {
  const router = useRouter();
  const [dialogOpen, setDialogOpen] = useState(false);
  const [expanded, setExpanded] = useState(false);

  const citedIDs = signal.citedItemIDs ?? [];
  const citedItems = citedIDs
    .map((id) => itemsByID?.get(id))
    .filter((it): it is ItemRecord => !!it);

  return (
    <div
      className={`border-l-4 rounded border border-zinc-800 bg-zinc-900/40 p-3 ${outcomeAccent[signal.outcome]}`}
      id={`signal-${signal.id}`}
    >
      <div className="flex items-center justify-between gap-3">
        <div className="text-xs text-zinc-500">
          <span className="font-medium text-zinc-200">{signal.outcome}</span>
          {" · "}
          {new Date(signal.createdAt).toLocaleString()}
          {signal.manuallyStarted && " · (manual)"}
        </div>
        <div>
          {signal.investigationID ? (
            <Link
              href={`/investigations/?id=${encodeURIComponent(signal.investigationID)}`}
              className="text-sm text-zinc-300 underline-offset-2 hover:text-zinc-100 hover:underline"
            >
              → Investigation
            </Link>
          ) : (signal.outcome === "unclear" || signal.outcome === "disabled" || signal.outcome === "failed" || signal.outcome === "dismissed" || signal.outcome === "proposed") && (
            <button
              type="button"
              onClick={() => setDialogOpen(true)}
              className="inline-flex items-center gap-1.5 rounded border border-zinc-700 bg-zinc-900/60 px-3 py-1.5 text-xs text-zinc-300 transition hover:border-zinc-600 hover:text-zinc-100"
            >
              Start investigation
              <ArrowRightIcon className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
      </div>
      {signal.briefing && (
        <div className="mt-1.5 text-sm text-zinc-300">{signal.briefing}</div>
      )}
      {signal.reason && (
        <div className="mt-1 text-sm text-zinc-500">reason: {signal.reason}</div>
      )}
      {signal.outcome === "failed" && signal.errorMessage && (
        <div className="mt-2 rounded border border-red-900/60 bg-red-950/40 px-2 py-1.5 text-xs text-red-300">
          <span className="font-medium">error:</span> {signal.errorMessage}
        </div>
      )}
      {signal.dismissedWikiSlugs?.length ? (
        <div className="mt-2 flex flex-wrap gap-1">
          {signal.dismissedWikiSlugs.map((s) => (
            <Link
              key={s}
              href={`/wiki/entries/${s}`}
              className="inline-flex items-center gap-1.5 rounded-full bg-zinc-800 px-2 py-0.5 text-xs text-zinc-400 transition hover:text-zinc-200"
            >
              {s}
            </Link>
          ))}
        </div>
      ) : null}

      {citedIDs.length > 0 && (
        <div className="mt-2">
          <button
            type="button"
            onClick={() => setExpanded((v) => !v)}
            className="text-xs text-zinc-500 transition hover:text-zinc-300"
          >
            {expanded ? "▾" : "▸"} {citedIDs.length} cited item{citedIDs.length === 1 ? "" : "s"}
          </button>
          {expanded && (
            <ul className="mt-2 space-y-2">
              {citedItems.length === 0 ? (
                <li className="rounded border border-zinc-800 bg-zinc-950 p-2 text-xs text-zinc-500">
                  Items not loaded — they may have been pruned or the watch's items panel hasn&apos;t fetched yet.
                </li>
              ) : (
                citedItems.map((it) => <CitedItem key={it.id} item={it} />)
              )}
            </ul>
          )}
        </div>
      )}

      {dialogOpen && (
        <StartFromSignalDialog
          watchID={signal.watchID}
          signalID={signal.id}
          initialClusters={signal.clusters}
          onClose={(invID) => {
            setDialogOpen(false);
            if (invID) router.push(`/investigations/?id=${encodeURIComponent(invID)}`);
          }}
        />
      )}
    </div>
  );
}

function CitedItem({ item }: { item: ItemRecord }) {
  const snap = item.snapshot ?? {};
  // Heading priority: github gives title+body; slack gives only text.
  // If text is multi-line, the first line is the natural heading and
  // the rest becomes the body. Final fallback rather than "(no title)"
  // is the source kind + capture date — useless cards become at least
  // identifiable in the list.
  const text = (snap.text ?? "").trim();
  const firstLine = text.split(/\r?\n/, 1)[0];
  const heading = snap.title?.trim() || firstLine || `${item.sourceKind ?? "item"} · ${new Date(item.capturedAt).toLocaleString()}`;
  // Body: github gives explicit body; slack passes text — split off the
  // first line we already used as heading.
  const body = (snap.body?.trim() ?? "") || (text === firstLine ? "" : text.slice(firstLine.length).trim());
  const url = snap.url ?? snap.permalink;
  return (
    <li className="rounded border border-zinc-800 bg-zinc-950 p-3 text-sm">
      <div className="flex items-baseline justify-between gap-3">
        <div className="min-w-0 break-words font-medium text-zinc-200">{heading}</div>
        {url && (
          <a
            href={url}
            target="_blank"
            rel="noreferrer"
            className="inline-flex shrink-0 items-center gap-1 text-xs text-zinc-400 underline-offset-2 hover:text-zinc-200 hover:underline"
            title="Open the source message / issue"
          >
            open
            <ExternalLinkIcon className="h-3 w-3 opacity-60" />
          </a>
        )}
      </div>
      <div className="mt-0.5 text-xs text-zinc-500">
        {snap.author && <span>by {snap.author} · </span>}
        {new Date(item.capturedAt).toLocaleString()}
      </div>
      {body && (
        <pre className="mt-2 max-h-48 overflow-y-auto whitespace-pre-wrap break-words rounded bg-zinc-900/60 p-2 font-sans text-xs text-zinc-300">
          {body}
        </pre>
      )}
    </li>
  );
}
