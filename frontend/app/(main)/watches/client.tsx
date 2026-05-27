"use client";

import { useWatches } from "@/lib/use-watches";
import { WatchesList } from "@/components/WatchesList";
import { AllWatchesSignalsPanel } from "@/components/AllWatchesSignalsPanel";

export function WatchesClient() {
  const { watches, refresh } = useWatches();
  return (
    <main className="flex-1 overflow-y-auto">
      <div className="mx-auto max-w-6xl space-y-6 px-6 py-10">
        <h1 className="text-2xl font-semibold text-zinc-100">Watches</h1>
        <WatchesList watches={watches} onMutated={refresh} />
        <AllWatchesSignalsPanel watches={watches} />
      </div>
    </main>
  );
}
