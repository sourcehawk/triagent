"use client";

import { useState } from "react";
import { Bot } from "lucide-react";
import { api, ApiError, type Investigation } from "@/lib/api";
import { useDialog } from "@/lib/dialog";
import { labelFor } from "@/lib/sidebar-label";
import { EditIcon } from "@/components/shared/Icons";
import { formatCostUSD, formatTokens, totalTokens } from "@/lib/usage";
import { SidebarSyncIcon, formatRelative } from "./Sidebar.utils";

export type InvRowProps = {
  inv: Investigation;
  active: boolean;
  onSelect: (id: string) => void;
  onDelete: (id: string, ev: React.MouseEvent) => void;
  onRenamed: (updated: Investigation) => void;
};

export function InvRow({ inv, active, onSelect, onDelete, onRenamed }: InvRowProps) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(inv.label ?? "");
  const [saving, setSaving] = useState(false);
  const lbl = labelFor(inv);
  const dialog = useDialog();

  function startEdit(e: React.MouseEvent) {
    e.stopPropagation();
    setDraft(inv.label ?? "");
    setEditing(true);
  }

  async function commit() {
    const trimmed = draft.trim();
    if (!trimmed) {
      setEditing(false);
      return;
    }
    if (trimmed === (inv.label ?? "").trim()) {
      setEditing(false);
      return;
    }
    setSaving(true);
    try {
      const updated = await api.setInvestigationLabel(inv.id, trimmed);
      onRenamed(updated);
      setEditing(false);
    } catch (err) {
      // Stay in edit mode so the operator can fix and retry; surface
      // the failure as a toast so blur-triggered failures don't get
      // swallowed silently.
      const detail =
        err instanceof ApiError
          ? err.message
          : err instanceof Error
            ? err.message
            : String(err);
      dialog.notify({
        kind: "error",
        title: "Rename failed",
        body: detail,
        ttlMs: 6000,
      });
    } finally {
      setSaving(false);
    }
  }

  return (
    <li>
      {/* role=button (not a <button>) because the row hosts nested
          controls — rename / delete buttons and the rename <input> — and
          a <button> may not contain interactive descendants (invalid HTML
          + hydration error). tabIndex + onKeyDown keep keyboard activation;
          the guard restricts it to the row's own focus so Enter in the
          rename input doesn't also select the row. */}
      <div
        role="button"
        tabIndex={0}
        data-testid="triagent-investigation-row"
        data-investigation-id={inv.id}
        onClick={() => onSelect(inv.id)}
        onKeyDown={(e) => {
          if (e.target !== e.currentTarget) return;
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            onSelect(inv.id);
          }
        }}
        className={
          "group flex w-full cursor-pointer flex-col items-start gap-0.5 rounded px-2 py-1.5 text-left transition " +
          (active ? "bg-zinc-800/80" : "hover:bg-zinc-900")
        }
      >
        <div className="flex w-full items-baseline justify-between gap-2">
          {editing ? (
            <input
              autoFocus
              type="text"
              maxLength={80}
              value={draft}
              disabled={saving}
              onChange={(e) => setDraft(e.target.value)}
              onClick={(e) => e.stopPropagation()}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  void commit();
                } else if (e.key === "Escape") {
                  e.preventDefault();
                  setEditing(false);
                }
              }}
              onBlur={() => void commit()}
              className="min-w-0 flex-1 rounded border border-zinc-700 bg-zinc-950 px-1.5 py-0.5 text-sm text-zinc-100 focus:border-sky-600 focus:outline-none"
            />
          ) : (
            <span
              className={
                "flex min-w-0 flex-1 items-center gap-1.5 truncate text-sm group-hover:overflow-visible group-hover:whitespace-normal group-hover:break-words " +
                (lbl.placeholder ? "italic text-zinc-500" : "text-zinc-100")
              }
            >
              {inv.auto?.enabled && (
                // Pink while the auto-operator is actively driving
                // (started / resumed), zinc when paused (operator has
                // the wheel), emerald when the run finished cleanly,
                // and red on abort. This colour map mirrors the
                // SessionView header chip so the sidebar and the
                // session header agree at a glance.
                <Bot
                  data-testid="auto-glyph"
                  aria-label={`auto mode ${inv.auto.phase ?? "enabled"}`}
                  size={14}
                  className={
                    "shrink-0 " +
                    (inv.auto.phase === "started" || inv.auto.phase === "resumed"
                      ? "text-pink-500"
                      : inv.auto.phase === "paused"
                        ? "text-zinc-400"
                        : inv.auto.phase === "finished"
                          ? "text-emerald-500"
                          : inv.auto.phase === "aborted"
                            ? "text-red-500"
                            : "text-pink-500")
                  }
                />
              )}
              <span className="min-w-0 flex-1 truncate group-hover:overflow-visible group-hover:whitespace-normal group-hover:break-words">
                {lbl.text}
              </span>
            </span>
          )}
          <SidebarSyncIcon syncState={inv.syncState} />
          {!editing && (
            <button
              type="button"
              onClick={startEdit}
              aria-label="rename investigation"
              className="shrink-0 text-xs text-zinc-600 opacity-0 transition group-hover:opacity-100 hover:text-sky-400"
            >
              <EditIcon className="h-3.5 w-3.5" />
            </button>
          )}
          {!editing && (
            <button
              type="button"
              onClick={(e) => onDelete(inv.id, e)}
              aria-label="delete investigation"
              className="shrink-0 text-xs text-zinc-600 opacity-0 transition group-hover:opacity-100 hover:text-red-400"
            >
              ✕
            </button>
          )}
        </div>
        <StatusLine inv={inv} />
      </div>
    </li>
  );
}

export function StatusLine({ inv }: { inv: Investigation }) {
  let label = "ready";
  let cls = "text-zinc-500";
  if (inv.archived) {
    label = "archived";
    cls = "text-zinc-600";
  } else if (inv.streaming) {
    label = "streaming";
    cls = "text-sky-400";
  } else if (inv.started) {
    label = "idle";
    cls = "text-emerald-400";
  }
  const tokens = totalTokens(inv.usage);
  const cost = inv.costUsd ?? 0;
  const showUsage = tokens > 0 || cost > 0;
  return (
    <div className="flex w-full items-center gap-2 text-xs">
      <span className={cls}>{label}</span>
      <span className="text-zinc-700">·</span>
      <time className="text-zinc-600" dateTime={inv.createdAt}>
        {formatRelative(inv.createdAt)}
      </time>
      {inv.namespace && (
        <>
          <span className="text-zinc-700">·</span>
          <span className="truncate font-mono text-zinc-600 group-hover:overflow-visible group-hover:whitespace-normal group-hover:break-all">
            {inv.namespace}
          </span>
        </>
      )}
      {showUsage && (
        <span
          className="ml-auto shrink-0 font-mono text-zinc-500"
          title={`${formatTokens(tokens)} tokens · ${formatCostUSD(cost)} spent this session`}
        >
          {formatTokens(tokens)} tok · {formatCostUSD(cost)}
        </span>
      )}
    </div>
  );
}
