import type { Playbook, PlaybookListItem } from "./playbook";
import { parsePlaybookYAML } from "./playbook";

// extractOutgoing returns the ids referenced by the given playbook
// via delegate_to (any node) and handoff (terminal nodes). Each list
// is deduplicated; order is the iteration order of Object.entries
// over `nodes`. Empty arrays when the playbook has no outgoing links.
export function extractOutgoing(playbook: Playbook): {
  delegates: string[];
  handoffs: string[];
} {
  const delegates = new Set<string>();
  const handoffs = new Set<string>();
  for (const node of Object.values(playbook.nodes)) {
    if (node.delegate_to && typeof node.delegate_to === "string") {
      delegates.add(node.delegate_to);
    }
    if (Array.isArray(node.handoff)) {
      for (const id of node.handoff) {
        if (typeof id === "string") handoffs.add(id);
      }
    }
  }
  return {
    delegates: Array.from(delegates),
    handoffs: Array.from(handoffs),
  };
}

// findInverse iterates the loaded playbook list and reports which
// other playbooks reference `targetId` via delegate_to or handoff.
// Each entry is a {id, kind} pair so the UI can label the source of
// the link. Self-references (a playbook that lists itself in handoff
// or delegate_to) are filtered out — they're rare and would clutter
// the inverse list pointlessly.
//
// Items whose YAML fails to parse are silently skipped — the sidebar
// already surfaces a "broken" badge for those entries elsewhere.
export function findInverse(
  list: ReadonlyArray<PlaybookListItem>,
  targetId: string,
): { delegatedFrom: string[]; handoffsFrom: string[] } {
  const delegatedFrom: string[] = [];
  const handoffsFrom: string[] = [];
  for (const item of list) {
    if (item.id === targetId) continue;
    if (!item.yaml) continue;
    let pb: Playbook;
    try {
      pb = parsePlaybookYAML(item.yaml);
    } catch {
      continue;
    }
    let hitsDelegate = false;
    let hitsHandoff = false;
    for (const node of Object.values(pb.nodes)) {
      if (!hitsDelegate && node.delegate_to === targetId) hitsDelegate = true;
      if (!hitsHandoff && Array.isArray(node.handoff) && node.handoff.includes(targetId)) {
        hitsHandoff = true;
      }
      if (hitsDelegate && hitsHandoff) break;
    }
    if (hitsDelegate) delegatedFrom.push(item.id);
    if (hitsHandoff) handoffsFrom.push(item.id);
  }
  return { delegatedFrom, handoffsFrom };
}
