"use client";

export type WatchStatus = "healthy" | "unhealthy" | "disabled";

// Human-readable label + tooltip per status. "disabled" was hard to
// reason about without context — the operator can't tell whether the
// watch is failing or intentionally paused. The label says it's paused;
// the title on hover spells out how to resume.
const presentation: Record<WatchStatus, { label: string; cls: string; title: string }> = {
  healthy: {
    label: "polling",
    cls: "bg-emerald-900/50 text-emerald-300",
    title: "Polling the source on schedule. Items + signals are flowing.",
  },
  unhealthy: {
    label: "errors",
    cls: "bg-amber-900/50 text-amber-300",
    title: "Recent polls have failed. Check the launcher logs for the underlying error (typically a revoked token, missing repo permission, or rate limit).",
  },
  disabled: {
    label: "paused",
    cls: "bg-zinc-800 text-zinc-400",
    title: "Not polling. Edit the watch and toggle the Enabled flag to resume — the watermark is preserved so it picks up where it left off.",
  },
};

export function WatchStatusPill({ status }: { status: WatchStatus }) {
  const p = presentation[status];
  return (
    <span
      className={`inline-flex cursor-help items-center gap-1.5 rounded-full px-2 py-0.5 text-xs ${p.cls}`}
      title={p.title}
    >
      {p.label}
    </span>
  );
}
