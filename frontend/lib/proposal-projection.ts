import type { TranscriptItem } from "./events";

// buildLatestPayloadsByKey scans a transcript for tool_call entries
// matching the given tool name, runs each parseable result through
// `parse`, and returns one payload per distinct key — last occurrence
// wins. Output order is the first-seen-key insertion order, so
// re-drafts for the same key update the slot in place without
// reshuffling.
//
// Each consumer supplies its own `parse` so this helper stays neutral
// to payload shape (playbook vs wiki vs anything else). `parse` should
// return null for any payload it can't safely use; the entry is then
// skipped (same path as unparseable JSON).
export function buildLatestPayloadsByKey<P>(
  items: ReadonlyArray<TranscriptItem>,
  toolName: string,
  parse: (raw: unknown) => { key: string; payload: P } | null,
): P[] {
  // JS Map preserves insertion order; setting an existing key keeps
  // the slot. That gives us first-seen-order with latest-payload-wins
  // in one pass.
  const byKey = new Map<string, P>();
  for (const it of items) {
    if (it.kind !== "tool_call" || it.name !== toolName || !it.result) continue;
    let raw: unknown;
    try {
      raw = JSON.parse(it.result);
    } catch {
      continue;
    }
    const out = parse(raw);
    if (!out) continue;
    byKey.set(out.key, out.payload);
  }
  return Array.from(byKey.values());
}
