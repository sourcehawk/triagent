"use client";

import { WikiHome } from "@/components/wiki/WikiHome";
import { WikiSideNav } from "@/components/wiki/WikiSideNav";

export default function WikiPage() {
  return (
    <>
      <main className="flex-1 min-w-0 overflow-y-auto">
        <WikiHome />
      </main>
      <WikiSideNav className="w-64 shrink-0" />
    </>
  );
}
