"use client";

import { useCallback, useEffect, useState } from "react";
import { usePathname } from "next/navigation";
import { watchesAPI, type Watch, type SignalRecord, type ItemRecord } from "@/lib/api";
import { useDialog } from "@/lib/dialog";
import { useStream } from "@/lib/stream";
import { WatchDetailHeader } from "@/components/watches/WatchDetailHeader";
import { WatchSignalsPanel } from "@/components/watches/WatchSignalsPanel";
import { WatchItemsPanel } from "@/components/watches/WatchItemsPanel";
import { WatchQueueStrip } from "@/components/watches/WatchQueueStrip";
import { WatchIngestRunsPanel } from "@/components/watches/WatchIngestRunsPanel";

// Static export rewrites every /watches/<id>/ path to the same shell
// (generateStaticParams returns the "_" placeholder), so useParams() is
// not a reliable source of the real id at runtime. Parse the URL via
// usePathname() — same pattern SessionDoc uses for /sessions/<slug>/.
function watchIDFromPath(pathname: string): string {
  const m = /^\/watches\/([^/]+)(?:\/|$)/.exec(pathname);
  return m ? decodeURIComponent(m[1]) : "";
}

export function WatchDetailClient() {
  const pathname = usePathname() ?? "";
  const id = watchIDFromPath(pathname);
  const dialog = useDialog();
  const stream = useStream();
  const [watch, setWatch] = useState<Watch | null>(null);
  const [signals, setSignals] = useState<SignalRecord[]>([]);
  const [items, setItems] = useState<ItemRecord[]>([]);
  const [polling, setPolling] = useState(false);

  const refreshSignalsAndItems = useCallback(() => {
    if (!id || id === "_") return;
    watchesAPI.signals(id, { limit: 100 }).then(r => setSignals(r.signals)).catch(() => {});
    watchesAPI.items(id, { limit: 100 }).then(r => setItems(r.items)).catch(() => {});
  }, [id]);

  useEffect(() => {
    if (!id || id === "_") return;
    watchesAPI.list().then(r => setWatch(r.watches.find(w => w.id === id) ?? null));
    refreshSignalsAndItems();
  }, [id, refreshSignalsAndItems]);

  // Refetch when the launcher emits a watch-scoped event so the
  // signals / items lists stay live without the operator having to
  // reload the page. The poller emits item_captured / signal_created
  // on every successful poll.
  useEffect(() => {
    if (!id || id === "_") return;
    return stream.subscribe({ scope: "global" }, (env) => {
      if (
        (env.kind === "item_captured" && env.itemCaptured?.watchID === id) ||
        (env.kind === "signal_created" && env.signalCreated?.watchID === id) ||
        (env.kind === "watch_status" && env.watchStatus?.watchID === id)
      ) {
        refreshSignalsAndItems();
      }
    });
  }, [id, stream, refreshSignalsAndItems]);

  const handleToggleEnabled = useCallback(async (next: boolean) => {
    if (!id || id === "_") return;
    const updated = await watchesAPI.patch(id, { enabled: next });
    setWatch(updated);
  }, [id]);

  const handlePollNow = useCallback(async () => {
    if (!id || id === "_" || polling) return;
    setPolling(true);
    const pendingID = dialog.notify({
      kind: "pending",
      title: "Polling…",
      body: "Fetching new items from the source.",
    });
    try {
      const res = await watchesAPI.pollNow(id);
      refreshSignalsAndItems();
      dialog.update(pendingID, {
        kind: "success",
        title: "Poll complete",
        body: `${res.itemsCaptured} item${res.itemsCaptured === 1 ? "" : "s"}, ${res.signalsCreated} signal${res.signalsCreated === 1 ? "" : "s"} (${res.durationMs}ms)`,
        ttlMs: 5000,
      });
    } catch (e) {
      dialog.update(pendingID, {
        kind: "error",
        title: "Poll failed",
        body: e instanceof Error ? e.message : String(e),
        ttlMs: 8000,
      });
    } finally {
      setPolling(false);
    }
  }, [id, polling, dialog, refreshSignalsAndItems]);

  if (!watch) {
    return (
      <main className="flex-1 overflow-y-auto">
        <div className="mx-auto max-w-6xl px-6 py-10 text-sm text-zinc-500">Loading…</div>
      </main>
    );
  }
  return (
    <main className="flex-1 overflow-y-auto">
      <div className="mx-auto max-w-6xl space-y-4 px-6 py-10">
        <WatchDetailHeader
          watch={watch}
          onPollNow={handlePollNow}
          onToggleEnabled={handleToggleEnabled}
          polling={polling}
        />
        <WatchQueueStrip watchID={id} />
        <WatchSignalsPanel watchID={id} signals={signals} items={items} />
        <WatchItemsPanel watchID={id} items={items} />
        <WatchIngestRunsPanel watchID={id} ingestEnabled={!!watch.ingest?.enabled} />
      </div>
    </main>
  );
}
