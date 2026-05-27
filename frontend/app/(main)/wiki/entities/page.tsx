import { Suspense } from "react";
import { WikiEntityPageClient } from "./client";

// Next.js requires useSearchParams (used in WikiEntityPageClient) to be
// wrapped in a Suspense boundary for output: "export" static builds.
export default function WikiEntityPage() {
  return (
    <Suspense
      fallback={
        <main className="flex-1 min-w-0 overflow-y-auto">
          <div className="px-6 py-12 text-sm text-zinc-500">Loading…</div>
        </main>
      }
    >
      <WikiEntityPageClient />
    </Suspense>
  );
}
