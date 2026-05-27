"use client";

import { useSearchParams } from "next/navigation";
import { WikiEditor } from "@/components/wiki/WikiEditor";

export function WikiEntryPageClient() {
  const searchParams = useSearchParams();
  const slug = searchParams?.get("slug") ?? "";

  return (
    <main className="flex-1 min-w-0 overflow-y-auto">
      {slug ? (
        <WikiEditor rootKind="incident" rootId={slug} />
      ) : (
        <div className="px-6 py-12 text-sm text-zinc-500">
          No entry selected.
        </div>
      )}
    </main>
  );
}
