"use client";

import { useSearchParams } from "next/navigation";
import { WikiEditor } from "@/components/wiki/WikiEditor";

export function WikiEntityPageClient() {
  const searchParams = useSearchParams();
  const type = searchParams?.get("type") ?? "";
  const name = searchParams?.get("name") ?? "";

  return (
    <main className="flex-1 min-w-0 overflow-y-auto">
      {type && name ? (
        <WikiEditor rootKind="entity" rootType={type} rootName={name} />
      ) : (
        <div className="px-6 py-12 text-sm text-zinc-500">
          No entity selected.
        </div>
      )}
    </main>
  );
}
