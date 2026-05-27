"use client";

import {
  Component,
  useMemo,
  useState,
  type ErrorInfo,
  type ReactNode,
} from "react";
import dynamic from "next/dynamic";
import { parsePlaybookYAML, type Playbook } from "@/lib/playbook";
import { PlaybookGraph } from "../PlaybookGraph";
import type { ProposalDraftPayload } from "../ProposalCard";

// react-diff-viewer-continued is client-only and pulls in
// styled-components. Dynamic import keeps SSR clean.
const DiffViewer = dynamic(() => import("react-diff-viewer-continued"), {
  ssr: false,
  loading: () => (
    <div className="px-2 py-3 text-xs text-zinc-500">loading diff…</div>
  ),
});

type Tab = "diagram" | "yaml";

type Props = {
  payload: ProposalDraftPayload;
  // Initial tab. Auto-overridden to "yaml" if proposed YAML fails to
  // parse. Default: "diagram".
  defaultTab?: Tab;
};

export function ProposalBodyTabs({ payload, defaultTab = "diagram" }: Props) {
  const parsed = useMemo(() => parseSides(payload), [payload]);
  const proposedBroken = parsed.proposed === null;
  const baseBroken = parsed.base === "error";

  const [tab, setTab] = useState<Tab>(defaultTab);

  // When the proposed YAML can't parse, force the YAML tab regardless
  // of what `tab` says. Computing this as a derived value (rather than
  // calling setTab during render) avoids the StrictMode "update during
  // render" warning and makes the non-null guard on parsed.proposed
  // straightforward to read below.
  const effectiveTab: Tab = proposedBroken ? "yaml" : tab;

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-1 border-b border-zinc-800">
        <TabButton
          active={effectiveTab === "diagram"}
          disabled={proposedBroken}
          disabledTitle={
            proposedBroken
              ? "couldn't render graph — see YAML diff"
              : undefined
          }
          onClick={() => setTab("diagram")}
        >
          diagram
        </TabButton>
        <TabButton
          active={effectiveTab === "yaml"}
          onClick={() => setTab("yaml")}
        >
          YAML diff
        </TabButton>
      </div>
      {effectiveTab === "diagram" && parsed.proposed ? (
        <DiagramPane
          proposed={parsed.proposed}
          base={parsed.base === "error" ? undefined : parsed.base}
          baseBroken={baseBroken}
        />
      ) : (
        <YamlPane payload={payload} />
      )}
    </div>
  );
}

function TabButton({
  active,
  disabled,
  disabledTitle,
  onClick,
  children,
}: {
  active: boolean;
  disabled?: boolean;
  disabledTitle?: string;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      title={disabledTitle}
      onClick={onClick}
      className={
        "border-b-2 px-3 py-1.5 text-xs transition " +
        (disabled
          ? "cursor-not-allowed border-transparent text-zinc-600"
          : active
            ? "border-sky-500 text-zinc-100"
            : "border-transparent text-zinc-400 hover:text-zinc-200")
      }
    >
      {children}
    </button>
  );
}

function DiagramPane({
  proposed,
  base,
  baseBroken,
}: {
  proposed: Playbook;
  base: Playbook | undefined;
  baseBroken: boolean;
}) {
  return (
    <div className="flex h-[60vh] min-h-[280px] flex-col">
      {baseBroken && (
        <div className="mb-2 rounded border border-amber-900/60 bg-amber-950/30 px-2 py-1 text-xs text-amber-300">
          Base playbook YAML failed to parse — showing the proposed
          shape with everything marked added. Use the YAML diff tab if
          you need a literal compare.
        </div>
      )}
      <div className="min-h-0 flex-1">
        <PreviewGraphBoundary>
          <PlaybookGraph
            playbook={proposed}
            basePlaybook={base}
            selectedId={null}
            onSelect={() => {
              /* preview only */
            }}
            onOpenPlaybook={() => {
              /* preview only */
            }}
          />
        </PreviewGraphBoundary>
      </div>
    </div>
  );
}

function YamlPane({ payload }: { payload: ProposalDraftPayload }) {
  const isNew = !payload.base_yaml || payload.base_yaml.trim() === "";
  return (
    <div className="max-h-[60vh] overflow-y-auto rounded border border-zinc-800 bg-zinc-950/60">
      <DiffViewer
        oldValue={payload.base_yaml ?? ""}
        newValue={payload.new_yaml}
        splitView
        useDarkTheme
        hideLineNumbers={false}
        leftTitle={isNew ? "Empty" : "Current"}
        rightTitle={isNew ? "New" : "Proposed"}
        styles={{
          variables: {
            dark: {
              diffViewerBackground: "#09090b",
              gutterBackground: "#18181b",
              codeFoldBackground: "#18181b",
              emptyLineBackground: "#0a0a0b",
            },
          },
          contentText: { fontSize: "11px", lineHeight: "1.4" },
        }}
      />
    </div>
  );
}

// PreviewGraphBoundary catches render-time errors thrown by
// PlaybookGraph (or its dependencies). Without this an uncaught error
// in the proposal surface bubbles to Next's error handler. The
// fallback keeps the surrounding action UI usable so the operator can
// still approve/decline.
class PreviewGraphBoundary extends Component<
  { children: ReactNode },
  { err: Error | null }
> {
  constructor(props: { children: ReactNode }) {
    super(props);
    this.state = { err: null };
  }
  static getDerivedStateFromError(err: Error) {
    return { err };
  }
  componentDidCatch(err: Error, info: ErrorInfo) {
    console.error("ProposalBodyTabs graph render failed:", err, info);
  }
  render() {
    if (this.state.err) {
      return (
        <div className="p-3 text-xs text-amber-300">
          Couldn't render the proposed graph ({this.state.err.message}).
          Switch to the YAML diff tab to review — approve/decline still
          work either way.
        </div>
      );
    }
    return this.props.children;
  }
}

type ParsedSides = {
  // null → proposed YAML is malformed; render the YAML tab only.
  proposed: Playbook | null;
  // "error" → base YAML present but malformed (treat as no base + warn).
  // undefined → no base provided (new playbook).
  // Playbook → base parsed successfully.
  base: Playbook | undefined | "error";
};

function parseSides(payload: ProposalDraftPayload): ParsedSides {
  let proposed: Playbook | null = null;
  try {
    proposed = parsePlaybookYAML(payload.new_yaml);
  } catch {
    proposed = null;
  }

  let base: Playbook | undefined | "error";
  const raw = payload.base_yaml ?? "";
  if (raw.trim() === "") {
    base = undefined;
  } else {
    try {
      base = parsePlaybookYAML(raw);
    } catch {
      base = "error";
    }
  }
  return { proposed, base };
}
