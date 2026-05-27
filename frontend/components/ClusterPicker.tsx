"use client";

import { useEffect, useState } from "react";
import { api, ApiError, type Cluster } from "@/lib/api";
import { Spinner } from "./Spinner";
import { FilterableList } from "./FilterableList";

type Props = {
  onPick: (cluster: Cluster) => void;
};

export function ClusterPicker({ onPick }: Props) {
  const [clusters, setClusters] = useState<Cluster[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .listClusters()
      .then(setClusters)
      .catch((e) => setError(e instanceof ApiError ? e.message : String(e)));
  }, []);

  if (error) return <ErrorBox title="Failed to list clusters" message={error} />;
  if (clusters === null)
    return (
      <div className="flex items-center gap-2 text-zinc-400">
        <Spinner /> fetching clusters…
      </div>
    );

  return (
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
      onPick={onPick}
      placeholder="Filter clusters by name or ID…"
      renderItem={(c) => (
        <>
          <span className="font-medium text-zinc-100">{c.name}</span>
          <span className="font-mono text-xs text-zinc-500">{c.id}</span>
        </>
      )}
    />
  );
}

function ErrorBox({ title, message }: { title: string; message: string }) {
  return (
    <div className="rounded border border-red-900/60 bg-red-950/40 p-3">
      <h3 className="text-sm font-semibold text-red-300">{title}</h3>
      <p className="mt-1 text-sm text-red-200/80">{message}</p>
    </div>
  );
}
