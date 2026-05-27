"use client";

import { Suspense, useEffect, useState } from "react";
import { useRouter, usePathname, useSearchParams } from "next/navigation";
import { PlaybookList } from "@/components/PlaybookList";
import { PlaybookEditor } from "@/components/PlaybookEditor";
import { PLAYBOOK_TYPES_CHANGED } from "../layout";

export default function PlaybooksPage() {
  return (
    <Suspense fallback={null}>
      <PlaybooksPageInner />
    </Suspense>
  );
}

function PlaybooksPageInner() {
  const router = useRouter();
  const pathname = usePathname() ?? "";
  const params = useSearchParams();

  // Migrate stale `/playbooks/<id>` bookmarks to `?playbook=<id>`. The
  // Go SPA fallback serves `playbooks/index.html` for the legacy URL,
  // so the user lands here with `pathname === "/playbooks/<id>"` and
  // `params` empty. One redirect on mount restores the editor.
  useEffect(() => {
    const m = pathname.match(/^\/playbooks\/([^/]+)\/?$/);
    if (m && m[1] !== "_") {
      router.replace(`/playbooks?playbook=${encodeURIComponent(m[1])}`);
    }
  }, [pathname, router]);

  const [refreshNonce, setRefreshNonce] = useState(0);

  // Listen for the layout's "+ new type" event so the type filter
  // refreshes without a page reload (preserved from the prior
  // page.tsx).
  useEffect(() => {
    function bump() {
      setRefreshNonce((n) => n + 1);
    }
    window.addEventListener(PLAYBOOK_TYPES_CHANGED, bump);
    return () => window.removeEventListener(PLAYBOOK_TYPES_CHANGED, bump);
  }, []);

  const playbookId = params?.get("playbook");

  if (!playbookId) {
    return (
      <main className="flex-1 overflow-y-auto">
        <div className="mx-auto max-w-6xl px-6 py-10">
          <PlaybookList
            onOpen={(id) =>
              router.push(`/playbooks?playbook=${encodeURIComponent(id)}`)
            }
            onNew={() => {}}
            refreshNonce={refreshNonce}
            onMutated={() => setRefreshNonce((n) => n + 1)}
          />
        </div>
      </main>
    );
  }

  return (
    <main className="flex-1 overflow-y-auto">
      <PlaybookEditor
        id={playbookId}
        onBack={() => router.push("/playbooks")}
        onMutated={() => setRefreshNonce((n) => n + 1)}
        onOpenPlaybook={(nextID) =>
          // replace, not push: the __new → saved-id transition during
          // accept-from-__new should not pollute the history stack with
          // the empty-template intermediate state.
          router.replace(`/playbooks?playbook=${encodeURIComponent(nextID)}`)
        }
      />
    </main>
  );
}
