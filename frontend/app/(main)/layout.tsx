"use client";

import { useCallback, useEffect, useState } from "react";
import { useRouter, usePathname } from "next/navigation";
import { AppShell } from "@/components/AppShell";
import { NewPlaybookModal } from "@/components/NewPlaybookModal";
import { NewTypeModal } from "@/components/NewTypeModal";
import { NewWikiEntryModal } from "@/components/wiki/NewWikiEntryModal";
import { RepoSummaryStateProvider } from "@/components/RepoSummaryStateProvider";
import { CodefixPRStateProvider } from "@/components/CodefixPRStateProvider";
import { StreamProvider } from "@/lib/stream";
import { api } from "@/lib/api";

// Cross-component refresh signal: the layout owns the NewTypeModal
// mount but the playbook list / type filter live further down the
// tree (under <children>). Event-bus pattern keeps both sides
// decoupled — whoever cares about freshly-added types listens for
// this and refetches.
export const PLAYBOOK_TYPES_CHANGED = "c1:playbook-types-changed";
export const PLAYBOOKS_CHANGED = "c1:playbooks-changed";

export default function MainLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const pathname = usePathname() ?? "/";
  const router = useRouter();
  const [showNewPlaybook, setShowNewPlaybook] = useState(false);
  const [showNewType, setShowNewType] = useState(false);
  const [showNewWiki, setShowNewWiki] = useState(false);
  const [existingPlaybookIDs, setExistingPlaybookIDs] = useState<Set<string>>(
    new Set(),
  );

  // Refetch playbook id list when the new-playbook modal opens — the
  // paste-raw-YAML path uses it for collision validation.
  useEffect(() => {
    if (!showNewPlaybook) return;
    api
      .listPlaybooks()
      .then((list) => setExistingPlaybookIDs(new Set(list.map((p) => p.id))))
      .catch(() => setExistingPlaybookIDs(new Set()));
  }, [showNewPlaybook]);

  const onNew = useCallback(() => {
    if (pathname.startsWith("/playbooks")) {
      setShowNewPlaybook(true);
    } else if (pathname.startsWith("/wiki")) {
      setShowNewWiki(true);
    } else if (pathname.startsWith("/repos")) {
      // /repos's primary action is "+ add repository". The
      // ManageReposModal is owned by LinkedReposPanel; fire a
      // CustomEvent it listens for so we don't have to lift its
      // state up to this layout.
      if (typeof window !== "undefined") {
        window.dispatchEvent(new Event("triagent:open-manage-repos"));
      }
    } else if (pathname.startsWith("/mcp") || pathname.startsWith("/docs")) {
      // No creation flow on these surfaces; Sidebar suppresses the
      // button on /docs (showSidebar=false), and on /mcp the button
      // exists but we ignore it. Defensive no-op.
    } else {
      // Default — investigations: navigate home to start a fresh
      // pick-cluster flow. The "/" route handles the new-investigation
      // step machine internally.
      router.push("/");
    }
  }, [pathname, router]);

  // Sidebar selects an item — route per-pathname to the appropriate
  // canonical URL. Investigations and playbooks both live on dedicated
  // dynamic routes; AppShell's `activeId` is derived from pathname in
  // Sidebar itself, so we don't have to thread it through here.
  const onSelect = useCallback(
    (id: string) => {
      if (pathname.startsWith("/playbooks")) {
        router.push(`/playbooks?playbook=${encodeURIComponent(id)}`);
      } else {
        router.push(`/investigations/?id=${encodeURIComponent(id)}`);
      }
    },
    [pathname, router],
  );

  const showSidebar = !pathname.startsWith("/docs");

  return (
    <StreamProvider>
      <RepoSummaryStateProvider>
        <CodefixPRStateProvider>
      <AppShell
        activeId={null}
        onSelect={onSelect}
        onNew={onNew}
        onNewType={
          pathname.startsWith("/playbooks") ? () => setShowNewType(true) : undefined
        }
        showSidebar={showSidebar}
      >
        {children}
        {showNewPlaybook && (
          <NewPlaybookModal
            existingIds={existingPlaybookIDs}
            defaultType="investigation"
            onChooseBlank={() => {
              setShowNewPlaybook(false);
              router.push("/playbooks?playbook=__new");
            }}
            onSavedFromYAML={(id) => {
              setShowNewPlaybook(false);
              if (typeof window !== "undefined") {
                window.dispatchEvent(new Event("c1:playbooks-changed"));
              }
              router.push(`/playbooks?playbook=${encodeURIComponent(id)}`);
            }}
            onClose={() => setShowNewPlaybook(false)}
          />
        )}
        {showNewType && (
          <NewTypeModal
            onClose={() => setShowNewType(false)}
            // Don't auto-close on create. NewTypeModal's own "created"
            // state shows the success copy + the PR link the operator
            // wants to follow; closing it here would unmount that view
            // before they see it. Instead, fire a global event so the
            // playbooks page (mounted under children) refetches its
            // type list — without it, the new type only appears after
            // a manual page reload. The operator dismisses the modal
            // themselves via "done".
            onCreated={() => {
              if (typeof window !== "undefined") {
                window.dispatchEvent(new Event(PLAYBOOK_TYPES_CHANGED));
              }
            }}
          />
        )}
        {showNewWiki && (
          <NewWikiEntryModal
            open={showNewWiki}
            onClose={() => setShowNewWiki(false)}
            onCreated={(_session, slug) => {
              setShowNewWiki(false);
              router.push(`/wiki/entries/?slug=${encodeURIComponent(slug)}`);
            }}
          />
        )}
      </AppShell>
        </CodefixPRStateProvider>
      </RepoSummaryStateProvider>
    </StreamProvider>
  );
}
