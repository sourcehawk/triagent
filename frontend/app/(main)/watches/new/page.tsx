"use client";

import { useRouter } from "next/navigation";
import { WatchForm } from "@/components/watches/WatchForm";
import { watchesAPI } from "@/lib/api";

export default function NewWatchPage() {
  const router = useRouter();
  return (
    <main className="flex-1 overflow-y-auto">
      <div className="mx-auto max-w-6xl space-y-6 px-6 py-10">
        <h1 className="text-2xl font-semibold text-zinc-100">New watch</h1>
        <WatchForm onSubmit={async w => {
          const created = await watchesAPI.create(w);
          router.push(`/watches/${created.id}`);
        }} />
      </div>
    </main>
  );
}
