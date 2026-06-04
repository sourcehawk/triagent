"use client";

import { useRouter } from "next/navigation";
import { DocsView } from "@/components/docs/DocsView";

// /docs renders the docs surface defaulted to the "overview" section.
// Picking another section in DocsView's left rail navigates to the
// dedicated /docs/<section> route so the URL stays canonical and
// shareable. (main)/layout.tsx hides the global sidebar for /docs/*;
// DocsView owns its own left rail.
export default function DocsHome() {
  const router = useRouter();
  return (
    <main className="flex-1 overflow-y-auto">
      <DocsView
        active="overview"
        onSectionChange={(next) =>
          router.push(`/docs/${encodeURIComponent(next)}`)
        }
      />
    </main>
  );
}
