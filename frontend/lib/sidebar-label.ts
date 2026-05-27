import type { Investigation } from "./api";

// labelFor returns the rendered text + a placeholder flag. Callers
// style placeholder rows differently (italic, dim) so the operator
// reads them as "the agent hasn't phrased a label yet" rather than
// a real title.
export function labelFor(inv: Investigation): {
  text: string;
  placeholder: boolean;
} {
  const trimmed = inv.label?.trim();
  if (trimmed) return { text: trimmed, placeholder: false };
  return { text: "New Investigation", placeholder: true };
}

// matchesQuery is the in-memory filter predicate behind the sidebar
// search input. Substring match (lowercase) across the operator-
// meaningful fields. Empty query passes everything.
export function matchesQuery(inv: Investigation, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  const fields: Array<string | undefined> = [
    inv.label,
    inv.namespace,
    inv.incidentUrl,
    inv.slackChannelUrl,
    inv.notes,
  ];
  return fields.some((f) => (f ?? "").toLowerCase().includes(q));
}
