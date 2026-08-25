import { DEFAULT_PLAYBOOK_TYPE, type PlaybookListItem } from "./playbook";

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
