"use client";

import type { WikiEntityRef } from "@/lib/wiki-api";
import { EntityCard } from "./EntityCard";

type Props = {
  entities: WikiEntityRef[];
  // compact renders a single-column list per type group — suitable for
  // the right-sidebar layout where width is constrained.
  compact?: boolean;
};

const TYPE_ORDER = ["service", "error", "symptom", "component"] as const;

// Each entity card is ~36px tall + 4px gap = ~40px per row; cap each
// type group at 3 visible rows and let the remainder live in a
// scroll-on-overscroll region. The scrollbar is visually hidden so the
// dense sidebar stays clean — overflow is discoverable via mouse-wheel /
// trackpad. A "+N more" pill below the list signals the overflow exists.
const ROWS_VISIBLE = 3;
const ROW_PX = 40;

export function EntityBrowser({ entities, compact = false }: Props) {
  if (entities.length === 0) {
    return (
      <div className="rounded-lg border border-zinc-800 bg-zinc-900/40 px-5 py-8 text-center text-sm text-zinc-500">
        No entities in the vault yet.
      </div>
    );
  }

  // Group by type.
  const grouped = new Map<string, WikiEntityRef[]>();
  for (const e of entities) {
    const list = grouped.get(e.type) ?? [];
    list.push(e);
    grouped.set(e.type, list);
  }

  return (
    <div className="space-y-6">
      {TYPE_ORDER.map((type) => {
        const list = grouped.get(type);
        if (!list || list.length === 0) return null;
        const overflow = Math.max(0, list.length - ROWS_VISIBLE);
        const maxHeightPx = ROWS_VISIBLE * ROW_PX;
        return (
          <section key={type}>
            <div className="mb-2 flex items-baseline gap-2">
              <h3 className="text-xs font-semibold uppercase tracking-wide text-zinc-400">
                {type}s
              </h3>
              <span className="text-xs text-zinc-600">{list.length}</span>
              {overflow > 0 && (
                <span className="text-[10px] text-zinc-600">scroll for {overflow} more</span>
              )}
            </div>
            <div
              className={`scrollbar-hide overflow-y-auto ${
                compact
                  ? "grid grid-cols-1 gap-1"
                  : "grid grid-cols-1 gap-1.5 sm:grid-cols-2 lg:grid-cols-3"
              }`}
              style={{ maxHeight: maxHeightPx }}
            >
              {list.map((entity) => (
                <EntityCard key={`${entity.type}:${entity.name}`} entity={entity} />
              ))}
            </div>
          </section>
        );
      })}
    </div>
  );
}
