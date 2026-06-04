"use client";

import yaml from "js-yaml";
import { Markdown } from "@/components/shared/Markdown";

// splitFrontmatter pulls the leading `---` block off a markdown string and
// returns (rawYaml, body). If the document has no frontmatter delimiters,
// the whole thing is returned as the body.
export function splitFrontmatter(md: string): { fm: string; body: string } {
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
export type ParsedFrontmatter = {
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

export function parseFrontmatter(raw: string): ParsedFrontmatter | null {
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
export function ProposalPreview({ md }: { md: string }) {
  const { fm, body } = splitFrontmatter(md);
  const meta = parseFrontmatter(fm);
  return (
    <div className="space-y-3">
      {meta && <FrontmatterMeta meta={meta} />}
      <Markdown text={body || "_(empty body)_"} />
    </div>
  );
}

export function FrontmatterMeta({ meta }: { meta: ParsedFrontmatter }) {
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

export function StatusChip({ status }: { status: string }) {
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

export function SeverityChip({ severity }: { severity: string }) {
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

export function FmChipRow({
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
