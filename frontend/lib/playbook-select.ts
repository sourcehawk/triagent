import type { PlaybookTypeItem } from "./api";
import { DEFAULT_PLAYBOOK_TYPE, type PlaybookListItem, type PlaybookType } from "./playbook";

// selectablePlaybooks narrows the playbook catalog to entries an
// operator can start a session against: locked entries are the
// launcher's own metas (the guided entrypoint, closing offer, and
// sub-flows the walker delegates to), and disabled / broken entries
// can't be walked. Investigation-typed playbooks sort first, then
// other types, each group by id.
export function selectablePlaybooks(items: PlaybookListItem[]): PlaybookListItem[] {
  const typeOf = (p: PlaybookListItem) => p.type || DEFAULT_PLAYBOOK_TYPE;
  const rank = (p: PlaybookListItem) => (typeOf(p) === DEFAULT_PLAYBOOK_TYPE ? 0 : 1);
  return items
    .filter((p) => !p.locked && !p.disabled && p.source !== "broken")
    .sort(
      (a, b) =>
        rank(a) - rank(b) ||
        typeOf(a).localeCompare(typeOf(b)) ||
        a.id.localeCompare(b.id),
    );
}

export type PlaybookGroup = {
  name: PlaybookType;
  description: string;
  playbooks: PlaybookListItem[];
};

// groupPlaybooks buckets selectable playbooks by type, in the order
// selectablePlaybooks yields them (investigation first, then the rest
// alphabetically), and attaches each type's catalog description so the
// picker can explain the grouping. A type the catalog doesn't know
// still gets a group, with an empty description.
export function groupPlaybooks(
  items: PlaybookListItem[],
  types: PlaybookTypeItem[],
): PlaybookGroup[] {
  const describe = new Map(types.map((t) => [t.name, t.description]));
  const groups: PlaybookGroup[] = [];
  for (const p of selectablePlaybooks(items)) {
    const name = p.type || DEFAULT_PLAYBOOK_TYPE;
    let g = groups[groups.length - 1];
    if (!g || g.name !== name) {
      g = { name, description: describe.get(name) ?? "", playbooks: [] };
      groups.push(g);
    }
    g.playbooks.push(p);
  }
  return groups;
}
