"use client";

import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import dynamic from "next/dynamic";
import yaml from "js-yaml";
import {
  api,
  ApiError,
  type Capabilities,
  type WikiProposalApprovedResponse,
} from "@/lib/api";
import { Markdown } from "@/components/shared/Markdown";
import { Spinner } from "@/components/shared/Spinner";

// react-diff-viewer-continued is client-only and pulls in styled-components,
// so we lazy-import it via next/dynamic to keep the SSR pass clean.
const DiffViewer = dynamic(() => import("react-diff-viewer-continued"), {
  ssr: false,
  loading: () => (
    <div className="px-2 py-3 text-xs text-zinc-500">loading diff…</div>
  ),
});

// WikiEntityGraph drags in @xyflow/react and dagre — non-trivial bundle
// weight, and SSR for ReactFlow needs `window`. Dynamic + ssr:false
// keeps the proposal card cheap to mount when the operator never
// switches to the entities tab.
const WikiEntityGraph = dynamic(
  () =>
    import("@/components/wiki/WikiEntityGraph").then((m) => ({ default: m.WikiEntityGraph })),
  {
    ssr: false,
    loading: () => (
      <div className="px-2 py-3 text-xs text-zinc-500">loading graph…</div>
    ),
  },
);

// Module-level so the styles prop has a stable reference. The
// "diffViewer"/"previewMarkdown" useMemos below would otherwise keep
// seeing fresh object literals each render.
const DIFF_VIEWER_STYLES = {
  variables: {
    dark: {
      diffViewerBackground: "#09090b",
      gutterBackground: "#18181b",
      codeFoldBackground: "#18181b",
      emptyLineBackground: "#0a0a0b",
    },
  },
  contentText: { fontSize: "11px", lineHeight: "1.4" },
};

// Payload returned by the triagent-wiki/propose_wiki_draft tool.
// Keep this in lockstep with proposeWikiDraftOut in
// mcp/internal/wiki/tool_propose.go.
export type WikiProposalPayload = {
  kind: "wiki_proposal_draft";
  proposal_id: string;
  slug: string;
  base_md?: string;
  new_md: string;
  is_new: boolean;
  message?: string;
  timed_out?: boolean;
  // new_entities: every entity stub sibling the sub-agent wrote. Raw
  // markdown is surfaced in the raw view so an operator can audit
  // both the post-mortem and the entity descriptions in one place.
  new_entities?: WikiNewEntityStub[];
};

export type WikiNewEntityStub = {
  type: string;
  name: string;
  description?: string;
  raw_md: string;
};

type Status =
  // Initial state on mount: ask the server first so a chat reload
  // after the proposal was approved/declined elsewhere doesn't show
  // stale Approve/Decline buttons.
  | { kind: "checking" }
  | { kind: "pending" }
  | { kind: "approving" }
  | {
      kind: "approved";
      slug: string;
      path: string;
      commit: string;
      stubsCreated?: string[];
    }
  | { kind: "declining" }
  | { kind: "declined" }
  // Server has no draft and no resolution marker — actions are no
  // longer applicable but we can't tell which way it went.
  | { kind: "resolved-unknown" }
  | { kind: "error"; message: string; errors?: string[] };

type Props = {
  payload: WikiProposalPayload;
  capabilities: Capabilities;
  // Same handler ProposalCard uses — sends a follow-up message to the
  // agent so a decline-with-notes loops back through the chat.
  onSendRefinement: (text: string) => Promise<void> | void;
  // Forwarded by SessionView; retained as a prop so callers don't break.
  // Approve and plain-decline are safe mid-stream — they don't go through
  // SendFollowUp. Only decline-with-notes does, and that path detects a
  // 409 explicitly (see decline() below) instead of gating up-front.
  streaming?: boolean;
};

export function WikiProposalCard({
  payload,
  capabilities,
  onSendRefinement,
}: Props) {
  const [status, setStatus] = useState<Status>({ kind: "checking" });
  const [refinement, setRefinement] = useState("");
  // Inline error within the pending state — used when send-refinement
  // 409s mid-stream so the operator can fix and retry without losing
  // their notes. Promoted to a separate state from `status: "error"`
  // because the terminal error state replaces the action buttons,
  // which would strand the operator without a retry.
  const [actionError, setActionError] = useState<string | null>(null);
  const [view, setView] = useState<"preview" | "raw" | "diff" | "entities">(
    payload.is_new ? "preview" : "diff",
  );

  // On mount, consult the server's resolution ledger so chat reloads
  // after approve/decline render the outcome instead of stale buttons.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await api.getWikiProposal(payload.proposal_id);
        if (cancelled) return;
        if (res.status === "approved") {
          setStatus({
            kind: "approved",
            slug: res.slug ?? payload.slug,
            path: res.path ?? "",
            commit: res.commit ?? "",
            stubsCreated: res.stubs_created,
          });
        } else if (res.status === "declined") {
          setStatus({ kind: "declined" });
        } else {
          setStatus({ kind: "pending" });
        }
      } catch (e) {
        if (cancelled) return;
        if (e instanceof ApiError && e.status === 404) {
          setStatus({ kind: "resolved-unknown" });
          return;
        }
        // Transient error: fall back to pending so the operator can
        // still act; the approve/decline endpoints check the ledger.
        setStatus({ kind: "pending" });
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [payload.proposal_id, payload.slug]);
  const isUpdate =
    !payload.is_new && payload.base_md && payload.base_md.length > 0;

  // Capability gating: wiki vault must be ready to save locally.
  // gh auth is NOT needed for approve (local-only now).
  // Streaming is *not* a gate any more — approve writes to the vault
  // and never touches /messages; plain decline only writes a resolution
  // sidecar. The one mid-stream-sensitive path is decline-with-notes,
  // which detects a 409 from sendRefinement and surfaces an inline
  // retry rather than silently dropping the notes.
  const wikiOK = capabilities.wiki.valid;
  const canApprove = wikiOK;
  const canDecline = true;

  const disabledReason = !wikiOK
    ? `wiki vault not ready: ${capabilities.wiki.reason ?? "configure --wiki-path at launcher"}`
    : "";

  async function approve() {
    setActionError(null);
    setStatus({ kind: "approving" });
    try {
      const res = await api.approveWikiProposal(payload.proposal_id);
      if (res.ok) {
        setStatus({
          kind: "approved",
          slug: res.slug,
          path: res.path,
          commit: res.commit,
          stubsCreated: res.stubs_created,
        });
        // Tell any open WikiEditor on this page to refetch its root
        // draft so the agent's freshly-committed content shows up
        // without a manual reload. Window-level event keeps
        // ProposalCard / Editor decoupled — they aren't in a parent
        // chain we can hand a callback through.
        window.dispatchEvent(
          new CustomEvent("c1:wiki-approved", {
            detail: {
              slug: res.slug,
              path: res.path,
              stubsCreated: res.stubs_created ?? [],
            },
          }),
        );
      } else {
        setStatus({
          kind: "error",
          message: res.error ?? "approval failed",
          errors: res.errors,
        });
      }
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : String(e);
      setStatus({ kind: "error", message: msg });
    }
  }

  async function decline() {
    setActionError(null);
    // Order matters: when the operator has typed refinement notes, send
    // them FIRST. SendFollowUp returns 409 while claude is mid-turn —
    // if we declined locally first and then 409'd on the notes, the
    // proposal would be gone with the operator's notes silently dropped.
    // Sending notes first means a 409 keeps the card pending so the
    // operator can wait / stop the agent and retry without re-typing.
    const notes = refinement.trim();
    if (notes) {
      try {
        await onSendRefinement(notes);
      } catch (e) {
        if (e instanceof ApiError && e.status === 409) {
          setActionError(
            "Agent is mid-turn — your notes weren't sent. Wait for the agent to finish (or stop it from the composer), then click decline again. The proposal is still pending.",
          );
        } else {
          setActionError(e instanceof ApiError ? e.message : String(e));
        }
        return;
      }
    }
    setStatus({ kind: "declining" });
    try {
      // Dual-write the note: chat follow-up already sent above so the
      // master agent reads it in conversation; the note here lands in
      // the .resolved marker for any future sub-agent dispatch that
      // reads current proposal state.
      await api.declineWikiProposal(payload.proposal_id, notes || undefined);
      setStatus({ kind: "declined" });
      window.dispatchEvent(new CustomEvent("c1:wiki-proposals-changed"));
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : String(e);
      setStatus({ kind: "error", message: msg });
    }
  }

  // Memoise the heavy view bodies. The "decline + send notes"
  // textarea below this card lives in the same component, so every
  // keystroke re-renders WikiProposalCard. Without these memos each
  // keystroke would re-run Markdown parsing or recompute the full
  // markdown diff — perceptibly laggy on real wiki entries. React's
  // same-element bailout skips the children entirely when these
  // references are unchanged.
  const previewBody = useMemo(
    () => <ProposalPreview md={payload.new_md} />,
    [payload.new_md],
  );
  const diffBody = useMemo(
    () => (
      <DiffViewer
        oldValue={payload.base_md ?? ""}
        newValue={payload.new_md}
        splitView
        useDarkTheme
        hideLineNumbers={false}
        leftTitle="current"
        rightTitle="proposed"
        styles={DIFF_VIEWER_STYLES}
      />
    ),
    [payload.base_md, payload.new_md],
  );
  const entitiesBody = useMemo(
    () => (
      <WikiEntityGraph
        newMd={payload.new_md}
        baseMd={payload.base_md}
        slug={payload.slug}
      />
    ),
    [payload.new_md, payload.base_md, payload.slug],
  );

  return (
    <div className="rounded border border-emerald-900/60 bg-emerald-950/20 px-3 py-2">
      {/* Header */}
      <div className="mb-2 flex items-baseline justify-between gap-2">
        <div className="flex items-baseline gap-2">
          <span className="text-[10px] uppercase tracking-wide text-emerald-400/80">
            wiki proposal
          </span>
          <span className="font-mono text-xs text-zinc-100">
            {payload.slug}
          </span>
          <span className="font-mono text-[10px] text-zinc-500">
            {payload.is_new ? "new" : "update"}
          </span>
        </div>
        <div className="flex items-center gap-2 text-[10px] text-zinc-500">
          <button
            type="button"
            onClick={() => setView("preview")}
            className={
              view === "preview" ? "text-zinc-200" : "hover:text-zinc-300"
            }
          >
            preview
          </button>
          <span>·</span>
          <button
            type="button"
            onClick={() => setView("raw")}
            className={
              view === "raw" ? "text-zinc-200" : "hover:text-zinc-300"
            }
          >
            raw
          </button>
          {isUpdate && (
            <>
              <span>·</span>
              <button
                type="button"
                onClick={() => setView("diff")}
                className={
                  view === "diff" ? "text-zinc-200" : "hover:text-zinc-300"
                }
              >
                diff
              </button>
            </>
          )}
          <span>·</span>
          <button
            type="button"
            onClick={() => setView("entities")}
            className={
              view === "entities" ? "text-zinc-200" : "hover:text-zinc-300"
            }
          >
            entities
          </button>
        </div>
      </div>

      {/* Content. The entities pane self-sizes via the graph's fixed
          60vh; for the markdown panes we cap height + scroll. */}
      <div
        className={
          view === "entities"
            ? "mb-2 rounded border border-zinc-800 bg-zinc-950/60"
            : "mb-2 max-h-[60vh] overflow-y-auto rounded border border-zinc-800 bg-zinc-950/60 px-2 py-2"
        }
      >
        {view === "preview" && previewBody}
        {view === "raw" && (
          <RawFiles payload={payload} />
        )}
        {view === "diff" && diffBody}
        {view === "entities" && entitiesBody}
      </div>

      {/* Footer */}
      {status.kind === "checking" && (
        <div className="flex items-center gap-2 text-xs text-zinc-500">
          <Spinner className="h-3 w-3" /> checking proposal status…
        </div>
      )}
      {status.kind === "pending" && (
        <div className="space-y-2">
          <textarea
            value={refinement}
            onChange={(e) => setRefinement(e.target.value)}
            placeholder="optional: ask the agent to refine before declining (e.g. 'tighten lessons section', 'add a link to the runbook')"
            rows={2}
            className="w-full resize-y rounded border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-100 placeholder-zinc-600 focus:border-zinc-600 focus:outline-none"
          />
          <div className="flex items-center justify-between gap-2">
            <p className="text-[10px] text-zinc-500">
              Approving saves{" "}
              <span className="font-mono text-zinc-300">
                entries/{payload.slug}.md
              </span>{" "}
              to the local wiki. PR push is a separate action from the entry
              page.
            </p>
            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={decline}
                disabled={!canDecline}
                className="rounded border border-zinc-700 px-2.5 py-1 text-xs text-zinc-300 transition hover:border-zinc-500 hover:text-zinc-100"
              >
                {refinement.trim() ? "decline + send notes" : "decline"}
              </button>
              <button
                type="button"
                onClick={approve}
                disabled={!canApprove}
                title={canApprove ? "" : disabledReason}
                className={
                  canApprove
                    ? "rounded bg-emerald-600 px-2.5 py-1 text-xs font-medium text-white transition hover:bg-emerald-500"
                    : "rounded bg-zinc-800 px-2.5 py-1 text-xs font-medium text-zinc-500 cursor-not-allowed"
                }
              >
                approve
              </button>
            </div>
          </div>
          {!canApprove && disabledReason && (
            <p className="text-[10px] text-amber-400/80">{disabledReason}</p>
          )}
          {actionError && (
            <div className="rounded border border-amber-900/60 bg-amber-950/30 px-2 py-1 text-[11px] text-amber-200/90">
              {actionError}
            </div>
          )}
        </div>
      )}

      {status.kind === "approving" && (
        <div className="flex items-center gap-2 text-xs text-zinc-400">
          <Spinner className="h-3 w-3" /> saving…
        </div>
      )}
      {status.kind === "declining" && (
        <div className="flex items-center gap-2 text-xs text-zinc-400">
          <Spinner className="h-3 w-3" /> declining…
        </div>
      )}
      {status.kind === "approved" && (
        <ApprovedSuccessBlock status={status} />
      )}
      {status.kind === "declined" && (
        <div className="rounded border border-zinc-800 bg-zinc-900/40 px-2 py-1 text-xs text-zinc-400">
          declined.
          {refinement.trim() && " refinement notes sent to the agent."}
        </div>
      )}
      {status.kind === "resolved-unknown" && (
        <div className="rounded border border-zinc-800 bg-zinc-900/40 px-2 py-1 text-xs text-zinc-400">
          This wiki proposal is no longer pending — it was approved or declined
          previously. Ask the agent for a fresh draft if you want to revisit.
        </div>
      )}
      {status.kind === "error" && (
        <div className="rounded border border-red-900/60 bg-red-950/40 px-2 py-1 text-xs text-red-200/90">
          {status.message}
          {status.errors && status.errors.length > 0 && (
            <ul className="ml-3 list-disc">
              {status.errors.map((e, i) => (
                <li key={i}>{e}</li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}

// ApprovedSuccessBlock shows the local-vault commit confirmation and
// prompts the operator to push from the incident detail page.
function ApprovedSuccessBlock({
  status,
}: {
  status: Extract<Status, { kind: "approved" }>;
}) {
  return (
    <div className="rounded border border-emerald-900/60 bg-emerald-950/40 px-3 py-2 text-xs text-emerald-200/90">
      <div className="mb-1">
        ✓ Saved to wiki vault as{" "}
        <span className="font-mono text-zinc-100">{status.path}</span>
      </div>
      <p className="text-[11px] text-zinc-400">
        Pushing as a PR to the upstream wiki repo is a separate action — open
        the entry in the wiki to push.
      </p>
      <div className="mt-1.5 flex flex-wrap items-center gap-2">
        <Link
          href={`/wiki/entries/?slug=${encodeURIComponent(status.slug)}`}
          className="rounded bg-emerald-600 px-2 py-0.5 text-[11px] font-medium text-white hover:bg-emerald-500"
        >
          open in wiki
        </Link>
        <span className="font-mono text-[10px] text-zinc-500">
          commit: {status.commit}
        </span>
      </div>
      {status.stubsCreated && status.stubsCreated.length > 0 && (
        <p className="mt-1 text-[10px] text-zinc-500">
          + created {status.stubsCreated.length} entity stub
          {status.stubsCreated.length === 1 ? "" : "s"}:{" "}
          {status.stubsCreated.join(", ")}
        </p>
      )}
    </div>
  );
}

// RawFiles renders every markdown file the proposal will produce — the
// entry draft plus any sibling entity stub files — with clear separators
// so an operator can audit the whole bundle at once.
function RawFiles({ payload }: { payload: WikiProposalPayload }) {
  const entryPath = `entries/${payload.slug}.md`;
  const stubs = payload.new_entities ?? [];
  return (
    <div className="space-y-3 text-[11px] text-zinc-300">
      <RawFileBlock path={entryPath} body={payload.new_md} role="entry" />
      {stubs.map((s) => (
        <RawFileBlock
          key={`${s.type}/${s.name}`}
          path={`entities/${s.type}/${s.name}.md`}
          body={s.raw_md}
          role="entity stub"
        />
      ))}
    </div>
  );
}

function RawFileBlock({
  path,
  body,
  role,
}: {
  path: string;
  body: string;
  role: string;
}) {
  return (
    <div>
      <div className="mb-1 flex items-baseline gap-2 border-b border-zinc-800 pb-1">
        <span className="text-[9px] uppercase tracking-wide text-zinc-500">
          {role}
        </span>
        <span className="font-mono text-[10px] text-zinc-300">{path}</span>
      </div>
      <pre className="whitespace-pre-wrap text-[11px] text-zinc-300">
        {body}
      </pre>
    </div>
  );
}

// splitFrontmatter pulls the leading `---` block off a markdown string and
// returns (rawYaml, body). If the document has no frontmatter delimiters,
// the whole thing is returned as the body.
function splitFrontmatter(md: string): { fm: string; body: string } {
  if (!md.startsWith("---")) return { fm: "", body: md };
  const rest = md.slice(3).replace(/^\r?\n/, "");
  const end = rest.search(/(^|\n)---\s*(\r?\n|$)/);
  if (end < 0) return { fm: "", body: md };
  const fm = rest.slice(0, end);
  // Trim the closing `---` line (and its newline) off the body.
  const after = rest.slice(end).replace(/^(\n)?---\s*(\r?\n)?/, "");
  return { fm, body: after.replace(/^\s+/, "") };
}

// ParsedFrontmatter mirrors mcp/internal/wiki.Frontmatter — keep aligned so
// the preview block renders the same fields the server validates.
type ParsedFrontmatter = {
  id?: string;
  title?: string;
  date?: string;
  status?: string;
  severity?: string;
  services?: string[];
  errors?: string[];
  symptoms?: string[];
  links?: {
    investigation?: string;
    incident_io?: string;
    slack_channel?: string;
    slack_message?: string;
  };
};

function parseFrontmatter(raw: string): ParsedFrontmatter | null {
  if (!raw.trim()) return null;
  try {
    const v = yaml.load(raw);
    if (v && typeof v === "object") return v as ParsedFrontmatter;
  } catch {
    /* unparseable — fall back to body-only preview */
  }
  return null;
}

// ProposalPreview shows the parsed frontmatter as a metadata block above the
// rendered body so the operator sees the same thing they'd see in the vault
// after approval — title, status/severity chips, entity references, links.
function ProposalPreview({ md }: { md: string }) {
  const { fm, body } = splitFrontmatter(md);
  const meta = parseFrontmatter(fm);
  return (
    <div className="space-y-3">
      {meta && <FrontmatterMeta meta={meta} />}
      <Markdown text={body || "_(empty body)_"} />
    </div>
  );
}

function FrontmatterMeta({ meta }: { meta: ParsedFrontmatter }) {
  const hasEntities =
    (meta.services && meta.services.length > 0) ||
    (meta.errors && meta.errors.length > 0) ||
    (meta.symptoms && meta.symptoms.length > 0);
  const links = meta.links ?? {};
  const linkRows: { label: string; href: string }[] = [];
  if (links.investigation) linkRows.push({ label: "investigation", href: links.investigation });
  if (links.incident_io) linkRows.push({ label: "incident.io", href: links.incident_io });
  if (links.slack_channel) linkRows.push({ label: "slack channel", href: links.slack_channel });
  if (links.slack_message) linkRows.push({ label: "slack message", href: links.slack_message });

  return (
    <div className="space-y-2 rounded border border-zinc-800 bg-zinc-950/40 px-3 py-2">
      {meta.title && (
        <div className="text-sm font-semibold text-zinc-100">{meta.title}</div>
      )}
      <div className="flex flex-wrap items-baseline gap-1.5 text-[10px]">
        {meta.status && <StatusChip status={meta.status} />}
        {meta.severity && <SeverityChip severity={meta.severity} />}
        {meta.date && (
          <span className="font-mono text-zinc-500">{meta.date}</span>
        )}
        {meta.id && (
          <span className="font-mono text-zinc-600">id: {meta.id}</span>
        )}
      </div>
      {hasEntities && (
        <div className="space-y-1">
          <FmChipRow label="services" values={meta.services} tone="sky" />
          <FmChipRow label="errors" values={meta.errors} tone="rose" />
          <FmChipRow label="symptoms" values={meta.symptoms} tone="amber" />
        </div>
      )}
      {linkRows.length > 0 && (
        <div className="flex flex-wrap gap-x-3 gap-y-1 text-[10px]">
          {linkRows.map((l) => (
            <a
              key={l.label}
              href={l.href}
              target="_blank"
              rel="noopener noreferrer"
              className="text-sky-400 hover:text-sky-300"
            >
              {l.label} ↗
            </a>
          ))}
        </div>
      )}
    </div>
  );
}

function StatusChip({ status }: { status: string }) {
  const tone =
    status === "resolved"
      ? "bg-emerald-900/40 text-emerald-300"
      : status === "wontfix"
        ? "bg-zinc-800 text-zinc-400"
        : "bg-amber-900/40 text-amber-300";
  return (
    <span className={`rounded px-1.5 py-0.5 font-mono uppercase ${tone}`}>
      {status}
    </span>
  );
}

function SeverityChip({ severity }: { severity: string }) {
  const tone =
    severity === "sev1"
      ? "bg-red-900/40 text-red-300"
      : severity === "sev2"
        ? "bg-orange-900/40 text-orange-300"
        : "bg-zinc-800 text-zinc-300";
  return (
    <span className={`rounded px-1.5 py-0.5 font-mono uppercase ${tone}`}>
      {severity}
    </span>
  );
}

function FmChipRow({
  label,
  values,
  tone,
}: {
  label: string;
  values?: string[];
  tone: "sky" | "rose" | "amber";
}) {
  if (!values || values.length === 0) return null;
  const cls = {
    sky: "bg-sky-900/40 text-sky-300",
    rose: "bg-rose-900/40 text-rose-300",
    amber: "bg-amber-900/40 text-amber-300",
  }[tone];
  return (
    <div className="flex flex-wrap items-baseline gap-1">
      <span className="text-[10px] uppercase text-zinc-600">{label}</span>
      {values.map((v) => (
        <span
          key={v}
          className={`rounded px-1.5 py-0.5 font-mono text-[10px] ${cls}`}
        >
          {v}
        </span>
      ))}
    </div>
  );
}
