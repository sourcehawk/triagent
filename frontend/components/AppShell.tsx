"use client";

import { TopNav } from "@/components/TopNav";
import { Sidebar } from "@/components/Sidebar";

// AppShell renders the chrome around any view: the global top nav and
// the global left sidebar. Both derive their state from the current
// pathname so callers don't have to thread a `view` prop. Per-view
// behaviour (which modal "+ new" opens, what activeId means) is
// supplied by the host route via the SidebarHandlers props.
//
// AppShell does NOT mount any modal — modals are per-view and live in
// the route's layout, where they have access to the view's local state
// (e.g. existingPlaybookIDs for NewPlaybookModal).
export type SidebarHandlers = {
  activeId: string | null;
  onSelect: (id: string) => void;
  onNew: () => void;
  onNewType?: () => void;
  refreshNonce?: number;
};

export type AppShellProps = SidebarHandlers & {
  // showSidebar=false suppresses the left rail. Used on /docs/* where
  // the docs surface owns its own left-rail (sections + per-page
  // outline) and a global sidebar would be double-railing.
  showSidebar?: boolean;
  // rightRail is rendered after children; used by /wiki for the
  // entity browser. Pass null/undefined to omit.
  rightRail?: React.ReactNode;
  children: React.ReactNode;
};

export function AppShell({
  activeId,
  onSelect,
  onNew,
  onNewType,
  refreshNonce,
  showSidebar = true,
  rightRail,
  children,
}: AppShellProps) {
  return (
    <div className="flex h-dvh flex-col">
      <TopNav />
      <div className="flex min-h-0 flex-1">
        {showSidebar && (
          <Sidebar
            activeId={activeId}
            onSelect={onSelect}
            onNew={onNew}
            onNewType={onNewType}
            refreshNonce={refreshNonce}
          />
        )}
        {children}
        {rightRail}
      </div>
    </div>
  );
}
