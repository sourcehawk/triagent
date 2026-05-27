"use client";

import { useEffect, useRef, useState } from "react";

export type WikiFilters = {
  q: string;
  services: string;
  errors: string;
  symptoms: string;
  severity: string;
  status: string;
  unsynced: boolean;
};

export const emptyFilters: WikiFilters = {
  q: "",
  services: "",
  errors: "",
  symptoms: "",
  severity: "",
  status: "",
  unsynced: false,
};

type Props = {
  onSearch: (filters: WikiFilters) => void;
};

const SEVERITY_OPTIONS = ["sev1", "sev2", "sev3"];
const STATUS_OPTIONS = ["open", "resolved", "wontfix"];

export function WikiSearch({ onSearch }: Props) {
  const [filters, setFilters] = useState<WikiFilters>(emptyFilters);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Debounce search calls: 300ms after the last change.
  useEffect(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => {
      onSearch(filters);
    }, 300);
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current);
    };
  }, [filters, onSearch]);

  function toggle(field: "severity" | "status", value: string) {
    setFilters((prev) => {
      const current = prev[field]
        ? prev[field].split(",").map((s) => s.trim()).filter(Boolean)
        : [];
      const next = current.includes(value)
        ? current.filter((v) => v !== value)
        : [...current, value];
      return { ...prev, [field]: next.join(",") };
    });
  }

  const activeSeverities = filters.severity
    ? filters.severity.split(",").filter(Boolean)
    : [];
  const activeStatuses = filters.status
    ? filters.status.split(",").filter(Boolean)
    : [];

  const hasFilters =
    filters.q !== "" ||
    filters.services !== "" ||
    filters.errors !== "" ||
    filters.symptoms !== "" ||
    filters.severity !== "" ||
    filters.status !== "" ||
    filters.unsynced;

  return (
    <div className="space-y-3">
      <input
        type="search"
        value={filters.q}
        onChange={(e) => setFilters((prev) => ({ ...prev, q: e.target.value }))}
        placeholder="Search incidents by title or body…"
        className="w-full rounded-md border border-zinc-700 bg-zinc-900 px-3 py-2 text-sm text-zinc-100 placeholder-zinc-500 outline-none focus:border-zinc-500 focus:ring-1 focus:ring-zinc-600"
      />
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-xs text-zinc-500">severity</span>
        {SEVERITY_OPTIONS.map((sev) => (
          <Chip
            key={sev}
            label={sev}
            active={activeSeverities.includes(sev)}
            color={severityColor(sev)}
            onClick={() => toggle("severity", sev)}
          />
        ))}
        <span className="ml-2 text-xs text-zinc-500">status</span>
        {STATUS_OPTIONS.map((st) => (
          <Chip
            key={st}
            label={st}
            active={activeStatuses.includes(st)}
            color="default"
            onClick={() => toggle("status", st)}
          />
        ))}
        <span className="ml-2 text-xs text-zinc-500">sync</span>
        <Chip
          label="unsynced"
          active={filters.unsynced}
          color="amber"
          onClick={() =>
            setFilters((prev) => ({ ...prev, unsynced: !prev.unsynced }))
          }
        />
        {hasFilters && (
          <button
            type="button"
            onClick={() => setFilters(emptyFilters)}
            className="ml-auto text-xs text-zinc-500 hover:text-zinc-300"
          >
            clear
          </button>
        )}
      </div>
    </div>
  );
}

function Chip({
  label,
  active,
  color,
  onClick,
}: {
  label: string;
  active: boolean;
  color: "red" | "amber" | "sky" | "default";
  onClick: () => void;
}) {
  const base =
    "rounded-full border px-2.5 py-0.5 text-xs font-medium transition cursor-pointer select-none";
  const colorMap: Record<string, string> = {
    red: active
      ? "border-red-700 bg-red-900/60 text-red-200"
      : "border-zinc-700 text-zinc-400 hover:border-red-800 hover:text-red-300",
    amber: active
      ? "border-amber-700 bg-amber-900/50 text-amber-200"
      : "border-zinc-700 text-zinc-400 hover:border-amber-800 hover:text-amber-300",
    sky: active
      ? "border-sky-700 bg-sky-900/50 text-sky-200"
      : "border-zinc-700 text-zinc-400 hover:border-sky-800 hover:text-sky-300",
    default: active
      ? "border-zinc-500 bg-zinc-700 text-zinc-100"
      : "border-zinc-700 text-zinc-400 hover:border-zinc-500 hover:text-zinc-200",
  };
  return (
    <button
      type="button"
      onClick={onClick}
      className={`${base} ${colorMap[color] ?? colorMap.default}`}
    >
      {label}
    </button>
  );
}

function severityColor(sev: string): "red" | "amber" | "sky" | "default" {
  switch (sev) {
    case "sev1":
      return "red";
    case "sev2":
      return "amber";
    case "sev3":
      return "sky";
    default:
      return "default";
  }
}
