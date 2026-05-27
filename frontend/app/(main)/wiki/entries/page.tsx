import { Suspense } from "react";
import { WikiEntryPageClient } from "./client";

// Next.js requires useSearchParams (used in WikiEntryPageClient) to be
// wrapped in a Suspense boundary for output: "export" static builds.
export default function WikiEntryPage() {
  return (
    <Suspense
      fallback={
        <main className="flex-1 min-w-0 overflow-y-auto">
          <div className="px-6 py-12 text-sm text-zinc-500">Loading…</div>
        </main>
      }
    >
      <WikiEntryPageClient />
    </Suspense>
  );
}
