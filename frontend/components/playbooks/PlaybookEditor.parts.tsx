import { type Capabilities } from "@/lib/api";
import { ProposalCard, type ProposalDraftPayload } from "@/components/playbooks/ProposalCard";
import { ChatBubbleIcon, GitHubIcon } from "@/components/shared/Icons";
import {
  BTN_GATED,
  BTN_SECONDARY,
  BTN_SECONDARY_ACTIVE,
} from "@/lib/buttons";
import { type SourceTag } from "./PlaybookEditor.reducer";

export function ViewTab({
  active,
  onClick,
  label,
  badge,
  trailing,
  disabled,
}: {
  active: boolean;
  onClick: () => void;
  label: string;
  badge?: React.ReactNode;
  // trailing renders inside the same tab "chip" but to the right of
  // the label — used for the proposal tab's discard ✕. Kept inside
  // the chip rather than separate so the visual association is
  // unambiguous.
  trailing?: React.ReactNode;
  disabled?: boolean;
}) {
  return (
    <div
      role="tab"
      aria-selected={active}
      aria-disabled={disabled || undefined}
      className={
        "inline-flex shrink-0 items-center gap-2 rounded-t border-b-2 px-3 py-1.5 text-sm font-medium transition " +
        (disabled
          ? "cursor-not-allowed border-transparent text-zinc-700"
          : active
            ? "border-sky-500 bg-zinc-900/40 text-zinc-100"
            : "cursor-pointer border-transparent text-zinc-500 hover:text-zinc-200")
      }
      onClick={() => {
        if (disabled) return;
        onClick();
      }}
    >
      {label}
      {badge}
      {trailing}
    </div>
  );
}

export function ProposalTabBody({
  payload,
  onResolved,
}: {
  payload: ProposalDraftPayload | undefined;
  onResolved: (
    p: ProposalDraftPayload,
    kind: "approved" | "declined",
  ) => void;
}) {
  if (!payload) {
    return (
      <div className="px-3 py-4 text-xs text-zinc-500">
        Proposal no longer pending — pick another tab.
      </div>
    );
  }
  return (
    <div className="h-full min-h-0 overflow-y-auto">
      <ProposalCard
        payload={payload}
        onSendRefinement={async () => {
          /* refinements go through the chat composer */
        }}
        onResolved={(kind) => onResolved(payload, kind)}
      />
    </div>
  );
}

export function ProposalTab({
  payload,
  active,
  onClick,
  onDiscard,
}: {
  payload: ProposalDraftPayload;
  active: boolean;
  onClick: () => void;
  onDiscard: (p: ProposalDraftPayload) => void | Promise<void>;
}) {
  return (
    <div
      role="tab"
      aria-selected={active}
      title={payload.playbook_id}
      onClick={onClick}
      className={
        "inline-flex shrink-0 max-w-[14ch] items-center gap-1 rounded-t border-b-2 px-3 py-1.5 text-sm font-medium transition cursor-pointer " +
        (active
          ? "border-sky-500 bg-zinc-900/40 text-zinc-100"
          : "border-transparent text-zinc-500 hover:text-zinc-200")
      }
    >
      <span className="truncate">AI: {payload.playbook_id}</span>
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          void onDiscard(payload);
        }}
        title="discard proposal"
        aria-label="discard proposal"
        className="-mr-1 ml-1 inline-flex h-5 w-5 items-center justify-center rounded-full text-zinc-500 transition hover:bg-rose-500/15 hover:text-rose-300"
      >
        <svg
          viewBox="0 0 16 16"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          className="h-3 w-3"
          aria-hidden
        >
          <path d="M4 4l8 8M12 4l-8 8" />
        </svg>
      </button>
    </div>
  );
}

export function ChatToggleButton({
  open,
  onClick,
  label = "chat",
}: {
  open: boolean;
  onClick: () => void;
  label?: string;
}) {
  return (
    <button
      type="button"
      data-testid="triagent-playbook-chat-toggle"
      onClick={onClick}
      aria-pressed={open}
      title={
        open
          ? "collapse chat (keeps session alive — toggle again to resume)"
          : "open editor chat — refine this playbook with the assistant"
      }
      className={open ? BTN_SECONDARY_ACTIVE : BTN_SECONDARY}
    >
      <ChatBubbleIcon className="h-3.5 w-3.5" />
      {label}
    </button>
  );
}

// PushPRButton renders the "push as PR" action and explains via a
// tooltip when it's disabled (gh missing, repo path missing, validation
// errors). Disabled-but-visible is intentional — operators should
// understand the feature exists and what to fix to unlock it, rather
// than wondering why a button is missing. System (locked) playbooks
// don't render this at all — the parent gates it.
export function PushPRButton({
  capabilities,
  disabled,
  noUpstreamDiff,
  onClick,
}: {
  capabilities: Capabilities | null;
  disabled: boolean;
  // True when the operator's draft body matches upstream (with
  // active+version stripped). Pushing would create a no-op PR, so we
  // lock the button and explain why.
  noUpstreamDiff: boolean;
  onClick: () => void;
}) {
  let blockReason: string | null = null;
  if (!capabilities) {
    blockReason = "checking capabilities…";
  } else if (!capabilities.gh.authenticated) {
    blockReason = capabilities.gh.reason ?? "gh CLI unavailable";
  } else if (!capabilities.repoPath.valid) {
    blockReason = capabilities.repoPath.reason ?? "repo path not configured";
  } else if (!(capabilities.repoPath.repo ?? "")) {
    // Local-only mode: the playbooks dir is a usable git checkout but
    // has no upstream remote, so a PR has nowhere to go.
    blockReason = "no upstream playbooks repo configured — set defaults.playbooks_repo in your profile and restart the launcher";
  } else if (disabled) {
    blockReason = "fix the validation errors first";
  } else if (noUpstreamDiff) {
    blockReason = "no diff against upstream — edit the playbook before pushing";
  }
  const isDisabled = blockReason !== null;
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={isDisabled}
      title={blockReason ?? "Push this playbook as a PR to your playbooks repo checkout"}
      className={isDisabled ? BTN_GATED : BTN_SECONDARY}
    >
      <GitHubIcon className="h-3.5 w-3.5" />
      push as PR
    </button>
  );
}

export function SourceBadge({ source }: { source: SourceTag }) {
  // Provenance only. "plugin" = upstream library (overridable);
  // "system" = launcher-bundled meta (locked); "user"/"override" =
  // local; "broken" stays as-is. The "is this synced with remote?"
  // question is answered separately by the unsynced cloud-up icon
  // on the playbook list card.
  const label =
    source === "plugin"
      ? "remote"
      : source === "system"
        ? "system"
        : source === "broken"
          ? "broken"
          : "local";
  const cls = {
    plugin: "bg-zinc-800 text-zinc-300",
    // System metas are launcher-owned — slate sky tone signals "this
    // is the framework, not your edits".
    system: "bg-sky-900/50 text-sky-200",
    user: "bg-emerald-900/50 text-emerald-300",
    override: "bg-emerald-900/50 text-emerald-300",
    broken: "bg-red-900/60 text-red-300",
  }[source];
  return (
    <span
      className={
        "rounded-full px-2 py-0.5 text-xs font-medium uppercase tracking-wide " +
        cls
      }
    >
      {label}
    </span>
  );
}
