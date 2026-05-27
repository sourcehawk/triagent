"use client";

import { useState } from "react";
import type { ItemRecord } from "@/lib/api";
import { watchesAPI } from "@/lib/api";

export function WatchItemsPanel({ watchID, items: initial }: { watchID: string; items: ItemRecord[] }) {
  const [open, setOpen] = useState(false);
  const [items, setItems] = useState(initial);
  const [showFiltered, setShowFiltered] = useState(false);

  async function toggleFiltered(next: boolean) {
    setShowFiltered(next);
    const r = await watchesAPI.items(watchID, { limit: 100, includeFiltered: next });
    setItems(r.items);
  }

  return (
    <section className="rounded border border-zinc-800">
      <header className="flex items-center justify-between border-b border-zinc-800 px-4 py-2">
        <button
          type="button"
          onClick={() => setOpen(o => !o)}
          className="text-sm font-medium text-zinc-200"
        >
          {open ? "▾" : "▸"} Items ({items.length})
        </button>
        {open && (
          <label className="flex cursor-pointer items-center gap-1.5 text-xs text-zinc-400">
            <input
              type="checkbox"
              checked={showFiltered}
              onChange={e => toggleFiltered(e.target.checked)}
              className="accent-zinc-400"
            />
            Show filtered
          </label>
        )}
      </header>
      {open && (
        <ul>
          {items.map(it => (
            <li
              key={it.id}
              className={`border-t border-zinc-800/60 px-4 py-2 text-sm hover:bg-zinc-900/40 ${it.filtered ? "italic opacity-60" : ""}`}
            >
              <div className={`break-words ${it.filtered ? "text-zinc-500" : "text-zinc-200"}`}>
                {it.snapshot.title ?? it.snapshot.text}
              </div>
              <div className="mt-0.5 break-words text-xs text-zinc-500">
                {new Date(it.capturedAt).toLocaleString()}
                {it.filtered && ` · filtered: ${it.filtered.summary}`}
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
