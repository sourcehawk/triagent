"use client";

import { useEffect, useState } from "react";
import { usePathname, useRouter } from "next/navigation";
import { WatchForm } from "@/components/watches/WatchForm";
import { watchesAPI, type Watch } from "@/lib/api";

// See note in ../client.tsx: useParams() returns the build-time
// placeholder under output:export. Parse the real id from the URL.
function watchIDFromPath(pathname: string): string {
  const m = /^\/watches\/([^/]+)\/edit\/?$/.exec(pathname);
  return m ? decodeURIComponent(m[1]) : "";
}

export function EditWatchClient() {
  const pathname = usePathname() ?? "";
  const id = watchIDFromPath(pathname);
  const router = useRouter();
  const [watch, setWatch] = useState<Watch | null>(null);
  useEffect(() => {
    if (!id || id === "_") return;
    watchesAPI.list().then(r => setWatch(r.watches.find(w => w.id === id) ?? null));
  }, [id]);
  if (!watch) {
    return (
      <main className="flex-1 overflow-y-auto">
        <div className="mx-auto max-w-6xl px-6 py-10 text-sm text-zinc-500">Loading…</div>
      </main>
    );
  }
  return (
    <main className="flex-1 overflow-y-auto">
      <div className="mx-auto max-w-6xl space-y-6 px-6 py-10">
        <h1 className="text-2xl font-semibold text-zinc-100">Edit watch</h1>
        <WatchForm initial={watch} submitLabel="Save changes" onSubmit={async body => {
          await watchesAPI.patch(id, body);
          router.push(`/watches/${id}`);
        }} />
      </div>
    </main>
  );
}
