"use client";

import { Suspense, useEffect } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { ReposIndexClient } from "./client";
import { RepoArchitectureView } from "@/components/repos/RepoArchitectureView";

export default function ReposPage() {
  return (
    <Suspense fallback={null}>
      <ReposPageInner />
    </Suspense>
  );
}

// Parses an "owner/name" query value. Returns null when malformed so
// the caller can fall back to the list view instead of rendering an
// architecture view with empty strings.
function parseRepoParam(v: string | null): { owner: string; name: string } | null {
  if (!v) return null;
  const trimmed = v.trim();
  if (!trimmed) return null;
  const slash = trimmed.indexOf("/");
  if (slash <= 0 || slash === trimmed.length - 1) return null;
  return {
    owner: decodeURIComponent(trimmed.slice(0, slash)),
    name: decodeURIComponent(trimmed.slice(slash + 1)),
  };
}

function ReposPageInner() {
  const router = useRouter();
  const pathname = usePathname() ?? "";
  const params = useSearchParams();

  // Migrate stale `/repos/<owner>/<name>` bookmarks to
  // `?repo=<owner>/<name>`. The Go SPA fallback serves
  // `repos/index.html` for the legacy URL, so the user lands here
  // with `pathname === "/repos/<owner>/<name>"` and `params` empty.
  // One redirect on mount restores the focused repo.
  useEffect(() => {
    const m = pathname.match(/^\/repos\/([^/]+)\/([^/]+)\/?$/);
    if (m && m[1] !== "_" && m[2] !== "_") {
      const repo = `${decodeURIComponent(m[1])}/${decodeURIComponent(m[2])}`;
      router.replace(`/repos?repo=${encodeURIComponent(repo)}`);
    }
  }, [pathname, router]);

  const focused = parseRepoParam(params?.get("repo") ?? null);

  if (focused) {
    return (
      <main className="flex-1 overflow-y-auto">
        <RepoArchitectureView owner={focused.owner} name={focused.name} />
      </main>
    );
  }
  return <ReposIndexClient />;
}
