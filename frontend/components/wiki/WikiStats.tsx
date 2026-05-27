"use client";

import type { WikiStats } from "@/lib/wiki-api";

type Props = {
  stats: WikiStats;
};

export function WikiStats({ stats }: Props) {
  const lastSeen = stats.last_promoted_at
    ? formatDate(stats.last_promoted_at)
    : "—";

  return (
    <div className="flex flex-wrap gap-4 rounded-lg border border-zinc-800 bg-zinc-900/60 px-5 py-4">
      <Stat label="entries" value={String(stats.entry_count)} />
      <StatDivider />
      <Stat label="services" value={String(stats.entity_count_by_type.service)} />
      <Stat label="errors" value={String(stats.entity_count_by_type.error)} />
      <Stat label="symptoms" value={String(stats.entity_count_by_type.symptom)} />
      <Stat label="components" value={String(stats.entity_count_by_type.component)} />
      <StatDivider />
      <Stat label="last promoted" value={lastSeen} />
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-xl font-semibold tabular-nums text-zinc-100">
        {value}
      </span>
      <span className="text-xs uppercase tracking-wide text-zinc-500">
        {label}
      </span>
    </div>
  );
}

function StatDivider() {
  return <div className="my-1 w-px self-stretch bg-zinc-800" />;
}

function formatDate(dateStr: string): string {
  // dateStr is YYYY-MM-DD from the vault frontmatter.
  try {
    const d = new Date(dateStr + "T00:00:00Z");
    return d.toLocaleDateString(undefined, {
      year: "numeric",
      month: "short",
      day: "numeric",
      timeZone: "UTC",
    });
  } catch {
    return dateStr;
  }
}
