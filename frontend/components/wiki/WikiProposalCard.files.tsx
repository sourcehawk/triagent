"use client";

import { type WikiProposalPayload } from "./WikiProposalCard";

// RawFiles renders every markdown file the proposal will produce — the
// entry draft plus any sibling entity stub files — with clear separators
// so an operator can audit the whole bundle at once.
export function RawFiles({ payload }: { payload: WikiProposalPayload }) {
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

export function RawFileBlock({
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
