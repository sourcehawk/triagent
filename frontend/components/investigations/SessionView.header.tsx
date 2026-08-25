"use client";

import { useState } from "react";
import Link from "next/link";
import { api, ApiError, type Investigation } from "@/lib/api";
import { type SessionStatus } from "@/lib/events";
import { labelFor } from "@/lib/sidebar-label";
import { ArrowLeftIcon, DocIcon, DownloadIcon } from "@/components/shared/Icons";
import { toolAnchorId } from "@/components/shared/ToolCard";
import { StatusPill } from "./SessionView.status";

export function Header({
  investigation,
  status,
  latestSummaryToolId,
}: {
  investigation: Investigation;
  status: SessionStatus;
  latestSummaryToolId: string | null;
}) {
  return (
    <header className="flex items-baseline justify-between gap-4">
      <div className="min-w-0">
        {/* Back link to the investigations home (mirrors the
            playbook/wiki editors' "back to <list>" affordance). */}
        <Link
          href="/"
          className="inline-flex items-center gap-1.5 text-sm text-zinc-400 transition hover:text-zinc-200"
        >
          <ArrowLeftIcon className="h-3.5 w-3.5" />
          back to investigations
        </Link>
        <HeaderTitle investigation={investigation} />
        {investigation.originatingSignal && (
          <Link
            href={`/watches/${encodeURIComponent(investigation.originatingSignal.watchID)}#signal-${encodeURIComponent(investigation.originatingSignal.signalID)}`}
            className="mt-1 inline-flex items-center gap-1.5 rounded border border-blue-900/60 bg-blue-950/40 px-2 py-1 text-xs text-blue-200/90 transition hover:bg-blue-950/70 hover:text-blue-100"
          >
            ← from watch / signal {investigation.originatingSignal.signalID.slice(0, 8)}
          </Link>
        )}
        {/* Suppress the "imported from" badge when the resolver
            reports synced — that case is "synced from upstream",
            which the sidebar checkmark already conveys, and showing
            both reads as "manually imported" which is misleading for
            upstream pulls. The badge stays for true peer-share
            imports (a teammate handed us a bundle that was never
            pushed). Reads through SyncState rather than the raw
            `pushed` flag so this view agrees with the sidebar — same
            single oracle. */}
        {investigation.importedFrom &&
          investigation.syncState.status !== "synced" && (
          <ImportedFromBadge
            from={investigation.importedFrom}
            at={investigation.importedAt}
          />
        )}
      </div>
      <div className="flex items-center gap-3">
        <StatusPill status={status} archived={investigation.archived} />
        {latestSummaryToolId && (
          <ViewLatestSummaryButton toolId={latestSummaryToolId} />
        )}
        <ExportSessionButton investigationId={investigation.id} />
      </div>
    </header>
  );
}

// ViewLatestSummaryButton scrolls the chat to the most recent
// summarize tool call. The summary block uses the same toolAnchorId
// scheme as ToolCard so the activity panel's flash-on-jump treatment
// applies here too.
export function ViewLatestSummaryButton({ toolId }: { toolId: string }) {
  const onClick = () => {
    const el = document.getElementById(toolAnchorId(toolId));
    if (!el) return;
    el.scrollIntoView({ behavior: "smooth", block: "center" });
    const flash = ["ring-2", "ring-amber-400/60", "ring-offset-2", "ring-offset-zinc-950"];
    el.classList.add(...flash);
    window.setTimeout(() => el.classList.remove(...flash), 1500);
  };
  return (
    <button
      type="button"
      onClick={onClick}
      title="Scroll to the most recent investigation summary"
      className="inline-flex items-center gap-1.5 rounded border border-amber-900/60 bg-amber-950/40 px-2 py-1 text-xs text-amber-200/90 transition hover:bg-amber-950/70 hover:text-amber-100"
    >
      <DocIcon className="h-3.5 w-3.5" />
      view latest summary
    </button>
  );
}

// ExportSessionButton downloads a share bundle for the active investigation.
// Sharing is allowed for live and archived sessions alike — the bundle is a
// snapshot of disk-state, so live sessions just snapshot whatever's been
// persisted up to now (the transcript is appended atomically per event).
export function ExportSessionButton({ investigationId }: { investigationId: string }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function exportBundle() {
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      const { blob, filename } = await api.exportInvestigation(investigationId);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      a.remove();
      // Revoke on the next tick so the browser has had a chance to start
      // the download. Doing it synchronously cancels the download in some
      // browsers.
      window.setTimeout(() => URL.revokeObjectURL(url), 0);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <button
      type="button"
      onClick={exportBundle}
      disabled={busy}
      title={error ?? "download a redacted share bundle for this investigation"}
      className={
        "inline-flex items-center gap-1.5 rounded border border-zinc-800 px-2 py-1 text-xs transition " +
        (error
          ? "text-red-300 hover:text-red-200"
          : "text-zinc-500 hover:text-zinc-200") +
        " disabled:cursor-not-allowed disabled:opacity-60"
      }
    >
      <DownloadIcon className="h-3.5 w-3.5" />
      {busy ? "exporting…" : "export"}
    </button>
  );
}

// HeaderTitle renders the session's display name + an optional namespace
// subtitle. Falls through labelFor so an unlabelled session reads as
// "New Investigation" (italic, dim) rather than a blank h2.
function HeaderTitle({ investigation }: { investigation: Investigation }) {
  const lbl = labelFor(investigation);
  return (
    <>
      <h2
        className={
          "mt-1 truncate text-sm font-medium " +
          (lbl.placeholder ? "italic text-zinc-500" : "text-zinc-200")
        }
      >
        {lbl.text}
      </h2>
      {investigation.namespace && (
        <p className="truncate font-mono text-xs text-zinc-500">
          {investigation.namespace}
        </p>
      )}
      {investigation.playbook && (
        <p
          className="truncate text-xs text-zinc-500"
          title="Playbook selected at session start"
        >
          playbook: <span className="font-mono">{investigation.playbook}</span>
        </p>
      )}
    </>
  );
}

// ImportedFromBadge surfaces the provenance of a session that came in via
// share-bundle import. Subtle pill under the header subtitle so the
// receiver knows at a glance the transcript wasn't produced by their
// launcher.
function ImportedFromBadge({
  from,
  at,
}: {
  from: NonNullable<Investigation["importedFrom"]>;
  at: string | undefined;
}) {
  const when = at ? formatDate(at) : null;
  const subject = from.namespace;
  return (
    <p className="mt-1 inline-flex items-center gap-1.5 rounded border border-amber-900/40 bg-amber-950/30 px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-amber-200/80">
      <span aria-hidden>↓</span>
      <span>
        imported from <span className="font-mono normal-case">{subject}</span>
        {when ? ` on ${when}` : ""}
      </span>
    </p>
  );
}

function formatDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}
