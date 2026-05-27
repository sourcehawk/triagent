"use client";

import { Suspense, useEffect } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { MCPCatalogView } from "@/components/MCPCatalogView";

export default function MCPPage() {
  return (
    <Suspense fallback={null}>
      <MCPPageInner />
    </Suspense>
  );
}

function MCPPageInner() {
  const router = useRouter();
  const pathname = usePathname() ?? "";
  const params = useSearchParams();

  // Migrate stale `/mcp/<server>` bookmarks to `?server=<server>`. The
  // Go SPA fallback serves `mcp/index.html` for the legacy URL, so the
  // user lands here with `pathname === "/mcp/<server>"` and `params`
  // empty. One redirect on mount restores the focused section.
  useEffect(() => {
    const m = pathname.match(/^\/mcp\/([^/]+)\/?$/);
    if (m && m[1] !== "_") {
      router.replace(`/mcp?server=${encodeURIComponent(m[1])}`);
    }
  }, [pathname, router]);

  const focused = params?.get("server") ?? null;

  return (
    <main className="flex min-h-0 flex-1 flex-col">
      <MCPCatalogView
        focusServer={focused}
        onFocusServer={(name) =>
          name
            ? router.push(`/mcp?server=${encodeURIComponent(name)}`)
            : router.push("/mcp")
        }
      />
    </main>
  );
}
