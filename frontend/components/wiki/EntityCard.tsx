"use client";

import Link from "next/link";
import type { WikiEntityRef } from "@/lib/wiki-api";

type Props = {
  entity: WikiEntityRef;
};

export function EntityCard({ entity }: Props) {
  return (
    <Link
      href={`/wiki/entities/?type=${encodeURIComponent(entity.type)}&name=${encodeURIComponent(entity.name)}`}
      className="group flex items-center justify-between gap-3 rounded-md border border-zinc-800 bg-zinc-900/40 px-3 py-2 text-sm transition hover:border-zinc-700 hover:bg-zinc-900"
    >
      <div className="flex min-w-0 items-center gap-2">
        <span className={`shrink-0 rounded px-1.5 py-0.5 text-xs font-medium ${typeColor(entity.type)}`}>
          {entity.type}
        </span>
        <span className="truncate font-mono text-zinc-100 group-hover:text-white">
          {entity.name}
        </span>
      </div>
      {entity.entry_count > 0 && (
        <span className="shrink-0 rounded-full bg-zinc-800 px-2 py-0.5 text-xs tabular-nums text-zinc-400">
          {entity.entry_count}
        </span>
      )}
    </Link>
  );
}

function typeColor(type: string): string {
  switch (type) {
    case "service":
      return "bg-sky-900/50 text-sky-300";
    case "error":
      return "bg-red-900/50 text-red-300";
    case "symptom":
      return "bg-amber-900/50 text-amber-300";
    case "component":
      return "bg-violet-900/50 text-violet-300";
    default:
      return "bg-zinc-800 text-zinc-400";
  }
}
