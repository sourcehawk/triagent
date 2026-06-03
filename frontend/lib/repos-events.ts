// Cross-surface refresh signal for linked-repo lists. The sidebar
// (PendingReposList / ManageReposModal) and the /repos page each render an
// independent list from the same /api/repos data, so a repo added or removed
// on one surface must tell the others to refetch — otherwise the change only
// appears after a full page reload.
//
// DOM events use the triagent: prefix (ADR-0005); the c1: events predate the
// project rename and aren't a template for new ones.
export const REPOS_CHANGED_EVENT = "triagent:repos-changed";

// notifyReposChanged fires the refresh signal. Call it after any successful
// add/remove. dispatchEvent is synchronous, so every mounted listener —
// including the caller's own — refetches within this call stack.
export function notifyReposChanged(): void {
  if (typeof window === "undefined") return;
  window.dispatchEvent(new Event(REPOS_CHANGED_EVENT));
}

// onReposChanged subscribes cb to the refresh signal and returns an
// unsubscribe fn — drop it straight into a useEffect cleanup.
export function onReposChanged(cb: () => void): () => void {
  if (typeof window === "undefined") return () => {};
  window.addEventListener(REPOS_CHANGED_EVENT, cb);
  return () => window.removeEventListener(REPOS_CHANGED_EVENT, cb);
}
