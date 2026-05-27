"use client";

import { useState } from "react";
import { api, ApiError, type Investigation } from "@/lib/api";
import { useDialog } from "@/lib/dialog";

export function ArchiveButton({
  investigation,
  onArchived,
}: {
  investigation: Investigation;
  onArchived: (next: Investigation) => void;
}) {
  const dialog = useDialog();
  const [busy, setBusy] = useState(false);
  if (investigation.archived) return null;

  async function go() {
    const ok = await dialog.confirm({
      title: "End this investigation?",
      body: "The chat will stop and the session becomes read-only. If you've configured an upstream sessions repo, you'll be able to push it as a PR.",
      confirmLabel: "Archive",
    });
    if (!ok) return;
    setBusy(true);
    try {
      const updated = await api.archiveInvestigation(investigation.id);
      onArchived(updated);
      // Notify the sidebar (separate React tree) so its status pill
      // flips from idle → archived without waiting for a full
      // /api/investigations refetch. SessionWorkspace dispatches the
      // same event for label / usage updates.
      if (typeof window !== "undefined") {
        window.dispatchEvent(
          new CustomEvent("triagent:investigation-changed", {
            detail: updated,
          }),
        );
      }
    } catch (e) {
      await dialog.alert({
        title: "Archive failed",
        body: e instanceof ApiError ? e.message : String(e),
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <button
      type="button"
      onClick={go}
      disabled={busy}
      className="rounded border border-zinc-700 px-2 py-1 text-xs text-zinc-200 transition hover:border-zinc-500 disabled:opacity-50"
      title="end this investigation"
    >
      {busy ? "archiving…" : "archive"}
    </button>
  );
}
