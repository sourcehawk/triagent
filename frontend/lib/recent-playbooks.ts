// Recent-playbooks tracks ids the operator has visited in the editor
// during this browser session. Backed by sessionStorage so a tab
// refresh preserves context but a fresh tab starts clean. MRU-first
// ordering, deduped, capped at MAX_ENTRIES; the oldest entry is
// evicted when capacity is reached.
//
// Pure functions — components consume via the useRecentPlaybooks hook
// in components/Sidebar.tsx.

export const RECENT_KEY = "triagent.recent-playbooks";
export const MAX_ENTRIES = 10;

function readRaw(): string[] {
  if (typeof window === "undefined") return [];
  const raw = window.sessionStorage.getItem(RECENT_KEY);
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((x): x is string => typeof x === "string");
  } catch {
    return [];
  }
}

function writeRaw(ids: string[]) {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.setItem(RECENT_KEY, JSON.stringify(ids));
  } catch {
    /* sessionStorage full or disabled — silently degrade */
  }
}

export function getRecent(): string[] {
  return readRaw();
}

// pushRecent prepends `id` to the recent list, dedupes (so revisiting
// a playbook moves it to the top rather than adding a duplicate
// entry), and caps the list at MAX_ENTRIES. Returns the new list so
// callers can reflect the update without re-reading.
export function pushRecent(id: string): string[] {
  if (!id) return readRaw();
  const prev = readRaw();
  const filtered = prev.filter((x) => x !== id);
  const next = [id, ...filtered].slice(0, MAX_ENTRIES);
  writeRaw(next);
  // Notify same-tab listeners — the storage event only fires across
  // tabs, so we fire a custom event for the sidebar to react to in
  // the same tab as the editor that just visited a playbook.
  if (typeof window !== "undefined") {
    window.dispatchEvent(new CustomEvent("triagent:recent-playbooks-changed"));
  }
  return next;
}

// removeRecent drops a single id (e.g. when a playbook is deleted)
// so the sidebar doesn't keep advertising it.
export function removeRecent(id: string): string[] {
  const prev = readRaw();
  const next = prev.filter((x) => x !== id);
  if (next.length === prev.length) return prev;
  writeRaw(next);
  if (typeof window !== "undefined") {
    window.dispatchEvent(new CustomEvent("triagent:recent-playbooks-changed"));
  }
  return next;
}

// clearRecent is exported for tests + a future "clear history" affordance.
export function clearRecent() {
  if (typeof window === "undefined") return;
  window.sessionStorage.removeItem(RECENT_KEY);
  window.dispatchEvent(new CustomEvent("triagent:recent-playbooks-changed"));
}
