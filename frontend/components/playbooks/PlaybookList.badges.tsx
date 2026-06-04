import { type PlaybookListItem } from "@/lib/api";

// SourceBadge answers "where does the active version live?":
// "remote" = upstream-only (the operator hasn't authored or
// overridden it locally), "local" = there's a user file (whether
// it's a fresh id or an override of an upstream one). Collapsed
// from the previous user/override split because the more useful
// distinction (does it match remote?) is carried by the unsynced
// warning icon next to the id.
export function SourceBadge({ source }: { source: PlaybookListItem["source"] }) {
  const { label, cls } = sourceBadgeStyle(source);
  return (
    <span
      className={
        "rounded-full px-2 py-0.5 text-xs font-medium uppercase tracking-wide " +
        cls
      }
      title={
        source === "plugin"
          ? "lives in the upstream playbooks repo"
          : source === "system"
            ? "ships bundled with the launcher (locked — non-overridable)"
            : source === "broken"
              ? "file on disk could not be parsed"
              : "lives in the local user playbooks dir (override or fresh id)"
      }
    >
      {label}
    </span>
  );
}

function sourceBadgeStyle(source: PlaybookListItem["source"]): {
  label: string;
  cls: string;
} {
  switch (source) {
    case "plugin":
      return { label: "remote", cls: "bg-zinc-800 text-zinc-300" };
    case "system":
      // Sky tone for system metas — visually distinct from "remote
      // upstream library" so the lock signal reads at a glance.
      return { label: "system", cls: "bg-sky-900/50 text-sky-200" };
    case "user":
    case "override":
      return { label: "local", cls: "bg-emerald-900/50 text-emerald-300" };
    case "broken":
      return { label: "broken", cls: "bg-red-900/60 text-red-300" };
  }
}

// StatusPill is the always-visible "active / disabled" affirmation.
// Lives next to the SourceBadge so every card carries one of two
// signals — no more reading the absence of a disabled badge as
// "active". Mirrors the editor's StatusBadge visually (switch-track
// + label) so the same playbook reads the same way in the list and
// in the open editor — only the editor variant is interactive.
export function StatusPill({ disabled }: { disabled: boolean }) {
  return (
    <span
      aria-label={disabled ? "disabled" : "active"}
      className={
        "inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs font-medium uppercase tracking-wide " +
        (disabled
          ? "border-zinc-700 bg-zinc-800/60 text-zinc-400"
          : "border-emerald-700/60 bg-emerald-900/40 text-emerald-300")
      }
    >
      <span
        aria-hidden
        className={
          "relative inline-block h-2.5 w-5 rounded-full " +
          (disabled ? "bg-zinc-700" : "bg-emerald-500/70")
        }
      >
        <span
          className={
            "absolute top-[2px] h-1.5 w-1.5 rounded-full bg-zinc-100 " +
            (disabled ? "left-[2px]" : "left-[12px]")
          }
        />
      </span>
      <span className={disabled ? "line-through decoration-zinc-600" : ""}>
        {disabled ? "disabled" : "active"}
      </span>
    </span>
  );
}

// TypeBadge renders the playbook's type (the directory it lives in
// upstream — investigation, general, system, …). Always visible so
// every card carries its category at a glance, with the source badge
// next to it answering "remote vs local" separately. Colour-codes
// the canonical types so investigation stays subtle and other slots
// stand out, but unknown values render in a neutral pill so newly
// added types Just Work.
export function TypeBadge({ type }: { type?: PlaybookListItem["type"] }) {
  const name = type || "investigation";
  const cls =
    name === "investigation"
      ? "bg-zinc-800 text-zinc-300"
      : name === "general"
        ? "bg-sky-900/50 text-sky-300"
        : "bg-indigo-900/50 text-indigo-300";
  return (
    <span
      className={
        "rounded-full px-2 py-0.5 text-xs font-medium uppercase tracking-wide " +
        cls
      }
      title={`type: ${name}`}
    >
      {name}
    </span>
  );
}
