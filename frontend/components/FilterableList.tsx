"use client";

import { useMemo, useState, type ReactNode } from "react";

// FilterableList is a generic search-input + scrollable list. Extracted from
// ClusterPicker so the SlackChannelPicker can reuse the same UX without
// duplicating styles. Pure presentational — caller owns the data and the
// item-level click handler.
type Props<T> = {
  items: readonly T[];
  filter: (item: T, query: string) => boolean;
  renderItem: (item: T) => ReactNode;
  onPick: (item: T) => void;
  itemKey: (item: T) => string;
  placeholder?: string;
  initialFilter?: string;
  autoFocus?: boolean;
  emptyMessage?: ReactNode;
  belowFilter?: ReactNode;  // persistent note rendered between input and list
  // Tailwind class for the dropdown's max-height. Defaults to a viewport-
  // relative value calibrated for the full-page cluster picker; tighter
  // surfaces (e.g. the watch form's two-column layout) override with a
  // smaller cap.
  listMaxHeightClass?: string;
};

export function FilterableList<T>({
  items,
  filter,
  renderItem,
  onPick,
  itemKey,
  placeholder,
  initialFilter = "",
  autoFocus = true,
  emptyMessage = "no matches",
  belowFilter,
  listMaxHeightClass = "max-h-[calc((100dvh-18rem)*0.8)]",
}: Props<T>) {
  const [query, setQuery] = useState(initialFilter);
  const filtered = useMemo(
    () => items.filter((item) => filter(item, query)),
    [items, filter, query],
  );

  return (
    <div className="space-y-2">
      <input
        autoFocus={autoFocus}
        type="text"
        placeholder={placeholder}
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        className="w-full rounded border border-zinc-800 bg-zinc-900 px-3 py-2 text-sm placeholder-zinc-500 focus:border-zinc-600 focus:outline-none"
      />
      {belowFilter && (
        <p className="text-[11px] text-zinc-500">{belowFilter}</p>
      )}
      <ul className={`${listMaxHeightClass} divide-y divide-zinc-800 overflow-y-auto rounded border border-zinc-800 bg-zinc-900/40`}>
        {filtered.length === 0 && (
          <li className="px-3 py-3 text-sm text-zinc-500">{emptyMessage}</li>
        )}
        {filtered.map((item) => (
          <li key={itemKey(item)}>
            <button
              type="button"
              onClick={() => onPick(item)}
              className="flex w-full items-baseline justify-between gap-4 px-3 py-2 text-left transition hover:bg-zinc-800/60"
            >
              {renderItem(item)}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
