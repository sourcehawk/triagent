import { Suspense } from "react";
import { InvestigationPageClient } from "./client";
import { Spinner } from "@/components/shared/Spinner";

// Next.js requires useSearchParams (used in InvestigationPageClient) to be
// wrapped in a Suspense boundary for output: "export" static builds.
export default function InvestigationsPage() {
  return (
    <Suspense
      fallback={
        <main className="flex-1 overflow-y-auto">
          <div className="mx-auto max-w-2xl px-6 py-12">
            <div className="flex items-center gap-2 text-sm text-zinc-400">
              <Spinner /> loading investigation…
            </div>
          </div>
        </main>
      }
    >
      <InvestigationPageClient />
    </Suspense>
  );
}
