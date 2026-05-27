"use client";

import type { WikiEntryHit } from "@/lib/wiki-api";
import { EntryRow } from "./EntryRow";

type Props = {
  hits: WikiEntryHit[];
  isFiltering: boolean;
};

export function EntryList({ hits, isFiltering }: Props) {
  if (hits.length === 0) {
    return (
      <div className="rounded-lg border border-zinc-800 bg-zinc-900/40 px-5 py-8 text-center text-sm text-zinc-500">
        {isFiltering
          ? "No entries match the current search or filters."
          : "No entries in the vault yet."}
      </div>
    );
  }

  return (
    <div className="space-y-2">
      {hits.map((hit) => (
        <EntryRow key={hit.id || hit.path} hit={hit} />
      ))}
    </div>
  );
}
