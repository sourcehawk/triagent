"use client";

import { useState } from "react";

// Mirrors the backend regex in mcp/internal/entities/entities.go
const NAME_PATTERN = /^[a-z0-9][a-z0-9-]*$/;

type Props = {
  label: string;
  values: string[];
  onChange: (next: string[]) => void;
  placeholder?: string;
};

export function EntityChipsInput({ label, values, onChange, placeholder }: Props) {
  const [draft, setDraft] = useState("");
  const [error, setError] = useState<string | null>(null);
  const inputID = `chip-input-${label}`;
  const errorID = `chip-error-${label}`;

  const tryAdd = () => {
    const v = draft.trim();
    if (!v) return;
    if (!NAME_PATTERN.test(v)) {
      setError(`"${v}" must be lowercase + hyphens (^[a-z0-9][a-z0-9-]*$)`);
      return;
    }
    if (values.includes(v)) {
      setError(`"${v}" is already in the list`);
      return;
    }
    onChange([...values, v]);
    setDraft("");
    setError(null);
  };

  const remove = (v: string) => onChange(values.filter((x) => x !== v));

  return (
    <div className="space-y-1">
      <label htmlFor={inputID} className="text-xs font-medium uppercase tracking-wide text-zinc-400">
        {label}
      </label>
      <div className="flex flex-wrap gap-1">
        {values.map((v) => (
          <span
            key={v}
            className="inline-flex items-center gap-1 rounded bg-zinc-800 px-2 py-0.5 text-xs"
          >
            <span className="font-mono text-zinc-200">{v}</span>
            <button
              type="button"
              aria-label={`Remove ${v}`}
              className="text-zinc-500 hover:text-zinc-100"
              onClick={() => remove(v)}
            >
              ×
            </button>
          </span>
        ))}
      </div>
      <input
        id={inputID}
        type="text"
        value={draft}
        placeholder={placeholder ?? "add entity…"}
        aria-describedby={error ? errorID : undefined}
        onChange={(e) => {
          setDraft(e.target.value);
          setError(null);
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === ",") {
            e.preventDefault();
            tryAdd();
          }
        }}
        // Commit-on-blur is intentional: power-users tab away after typing
        // and shouldn't lose a partial entry to a misclick.
        onBlur={tryAdd}
        className="w-full rounded border border-zinc-700 bg-zinc-900 px-2 py-1 text-sm font-mono text-zinc-200 placeholder:text-zinc-600 focus:border-zinc-500 focus:outline-none"
      />
      {error && <div id={errorID} className="text-xs text-rose-400">{error}</div>}
    </div>
  );
}
