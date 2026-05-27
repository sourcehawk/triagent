"use client";

import { useState } from "react";
import { api, ApiError } from "@/lib/api";
import type { PlaybookCommit } from "@/lib/playbook";

type Props = {
  playbookID: string;
  commits: PlaybookCommit[];
  // currentSha is the sha the editor is *displaying* (pinned via the
  // dropdown). On initial load this is the active sha so the active
  // row is selected by default.
  currentSha?: string;
  // activeSha is the sha that produced the currently-active YAML on
  // disk — derived once by the parent. The matching row gets a
  // "latest · …" prefix so the operator sees which commit IS the
  // active one without a separate "latest" option in the list.
  activeSha?: string;
  onPickCommit: (commit: PlaybookCommit) => void | Promise<void>;
  // onPickLatest fires when the operator picks the row that matches
  // activeSha. The parent re-fetches the playbook detail so source /
  // upstreamYAML refresh in step with the editor body — important for
  // the push-PR enable gate which compares the draft against
  // upstreamYAML.
  onPickLatest: () => void | Promise<void>;
  dirty: boolean;
};

// CommitsDropdown shows the most recent commits affecting this
// playbook (merged across upstream + local repos, newest first), with
// a "show more" button that lazy-loads older entries via the paged
// endpoint. The active commit is labelled "latest · …" inline rather
// than as a separate dropdown entry so re-selecting it from the list
// is the same gesture as picking "latest" was previously — no more
// "I picked the same sha but got a different state" surprise.
export function CommitsDropdown({
  playbookID,
  commits,
  currentSha,
  activeSha,
  onPickCommit,
  onPickLatest,
  dirty,
}: Props) {
  const [extra, setExtra] = useState<PlaybookCommit[]>([]);
  const [loading, setLoading] = useState(false);
  const [exhausted, setExhausted] = useState(false);
  const all = [...commits, ...extra];
  const selectedSha = currentSha ?? activeSha ?? "";
  const showMore = !exhausted && all.length > 0;

  async function loadMore() {
    const oldest = all[all.length - 1];
    if (!oldest) return;
    setLoading(true);
    try {
      const more = await api.listPlaybookCommits(playbookID, oldest.sha);
      if (!more || more.length === 0) {
        setExhausted(true);
      } else {
        setExtra((prev) => [...prev, ...more]);
      }
    } catch (e) {
      // 404 means we paged past the root commit — that's the normal
      // "exhausted" signal. Other API errors (network, 5xx) shouldn't
      // permanently lock the operator out — log and let the next click
      // retry. The toast surface is heavier than this component
      // warrants right now; a console warning is the floor.
      if (e instanceof ApiError && e.status === 404) {
        setExhausted(true);
      } else {
        console.warn("listPlaybookCommits failed:", e);
      }
    } finally {
      setLoading(false);
    }
  }

  if (all.length === 0) {
    return (
      <span className="text-xs text-zinc-500" title="No commits yet">
        —
      </span>
    );
  }

  return (
    <div className="flex items-center gap-1">
      <select
        value={selectedSha}
        onChange={(e) => {
          const v = e.target.value;
          // Picking the active commit's row goes through the
          // pickLatest path so source / upstreamYAML refresh and the
          // push-PR gate matches what the operator visually sees.
          if (activeSha && v === activeSha) {
            void onPickLatest();
            return;
          }
          const picked = all.find((c) => c.sha === v);
          if (picked) void onPickCommit(picked);
        }}
        title={
          dirty
            ? "switching commits will prompt before discarding edits"
            : "view a different commit"
        }
        className="w-44 max-w-[176px] rounded border border-zinc-800 bg-zinc-950 px-1.5 py-1 font-mono text-xs text-zinc-200 focus:border-zinc-600 focus:outline-none"
        style={{ textOverflow: "ellipsis" }}
      >
        {all.map((c) => {
          const isLatest = activeSha === c.sha;
          const summary = c.summary.slice(0, 40);
          return (
            <option key={c.sha} value={c.sha} title={c.summary}>
              {(isLatest ? "latest · " : "") +
                c.sha.slice(0, 7) +
                " · " +
                c.source +
                " · " +
                summary}
            </option>
          );
        })}
      </select>
      {showMore && (
        <button
          type="button"
          onClick={() => void loadMore()}
          disabled={loading}
          title="load older commits"
          className="w-10 shrink-0 rounded border border-zinc-800 bg-zinc-950 px-1.5 py-1 text-xs text-zinc-400 transition hover:border-zinc-600 hover:text-zinc-200 disabled:opacity-50"
        >
          {loading ? "…" : "more"}
        </button>
      )}
    </div>
  );
}
