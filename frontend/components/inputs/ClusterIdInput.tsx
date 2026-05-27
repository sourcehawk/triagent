"use client";

import { useEffect, useState } from "react";
import { api, ApiError, type Cluster } from "@/lib/api";
import { FilterableList } from "@/components/FilterableList";
import { Spinner } from "@/components/Spinner";
import type { InputProps, ScalarInputValue } from "./types";
import { interpolateHint } from "./types";

export function ClusterIdInput({ schema, value, onChange }: InputProps<ScalarInputValue>) {
  const hint = schema.hint ? interpolateHint(schema.hint, value) : "";
  return (
    <div className="space-y-3">
      <ClusterPicker
        value={value.value}
        onChange={(id) => onChange({ value: id })}
      />
      <label className="block">
        <span className="mb-1 block text-xs uppercase tracking-wide text-zinc-400">
          {schema.label}
        </span>
        <input
          type="text"
          value={value.value}
          onChange={(e) => onChange({ value: e.target.value })}
          placeholder={schema.placeholder}
          className="w-full rounded border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-100 placeholder-zinc-600 focus:border-zinc-600 focus:outline-none"
        />
        {hint && <p className="mt-1 whitespace-pre-line text-xs text-zinc-500">{hint}</p>}
      </label>
    </div>
  );
}

// ClusterPicker shows a button until the user opens it, then lazily
// fetches available clusters and renders them as a filterable list.
// Selecting a cluster propagates the cluster ID upward. The picker is
// purely a convenience — the operator can also type a cluster ID directly
// in the text field below, and may skip cluster selection entirely for
// scope-unknown investigations.
function ClusterPicker({ value, onChange }: { value: string; onChange: (id: string) => void }) {
  const [open, setOpen] = useState(false);
  const [clusters, setClusters] = useState<Cluster[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selectedName, setSelectedName] = useState<string>("");

  useEffect(() => {
    if (!open || clusters !== null || error !== null) return;
    api
      .listClusters()
      .then(setClusters)
      .catch((e) => setError(e instanceof ApiError ? e.message : String(e)));
  }, [open, clusters, error]);

  function pick(c: Cluster) {
    onChange(c.id);
    setSelectedName(c.name);
    setOpen(false);
  }

  return (
    <div className="space-y-1">
      <span className="mb-1 block text-xs uppercase tracking-wide text-zinc-400">
        cluster hint (optional)
      </span>
      {selectedName && (
        <div className="flex items-center justify-between rounded border border-zinc-700 bg-zinc-900/60 px-3 py-2 text-sm">
          <span className="text-zinc-100">{selectedName}</span>
          <button
            type="button"
            onClick={() => {
              onChange("");
              setSelectedName("");
            }}
            className="text-xs text-zinc-500 hover:text-zinc-300"
          >
            clear
          </button>
        </div>
      )}
      {!selectedName && !open && (
        <button
          type="button"
          onClick={() => setOpen(true)}
          className="w-full rounded border border-zinc-700 bg-zinc-900/60 px-3 py-2 text-left text-sm text-zinc-400 hover:border-zinc-600 hover:text-zinc-200"
        >
          Pick a cluster…
        </button>
      )}
      {!selectedName && open && error && (
        <p className="text-xs text-zinc-500">
          Could not load clusters ({error}) — enter a cluster ID below if known.
        </p>
      )}
      {!selectedName && open && !error && clusters === null && (
        <div className="flex items-center gap-2 text-xs text-zinc-500">
          <Spinner /> fetching clusters…
        </div>
      )}
      {!selectedName && open && !error && clusters !== null && (
        <FilterableList<Cluster>
          items={clusters}
          filter={(c, q) => {
            const needle = q.trim().toLowerCase();
            if (!needle) return true;
            return (
              c.name.toLowerCase().includes(needle) ||
              c.id.toLowerCase().includes(needle)
            );
          }}
          itemKey={(c) => c.name}
          onPick={pick}
          placeholder="Filter clusters by name or ID…"
          renderItem={(c) => (
            <>
              <span className="font-medium text-zinc-100">{c.name}</span>
              <span className="font-mono text-xs text-zinc-500">{c.id}</span>
            </>
          )}
        />
      )}
      <p className="text-xs text-zinc-500">
        Pick a cluster as a hint for the agent, or skip and fill in a cluster ID
        below. Leave both empty for a scope-unknown investigation.
      </p>
    </div>
  );
}
