"use client";

import { useEffect, useState } from "react";
import { api, type InputSchema, type PlaybookListItem, type PlaybookTypeItem, type PromOverride } from "@/lib/api";
import { groupPlaybooks } from "@/lib/playbook-select";
import { ArrowRightIcon } from "@/components/shared/Icons";
import { Spinner } from "@/components/shared/Spinner";
import { TextInput } from "@/components/inputs/TextInput";
import { UrlInput } from "@/components/inputs/UrlInput";
import { TextareaInput } from "@/components/inputs/TextareaInput";
import { ClusterIdInput } from "@/components/inputs/ClusterIdInput";
import { SlackChannelInput } from "@/components/inputs/SlackChannelInput";

export type FormSubmission = {
  inputs: Record<string, Record<string, unknown>>;
  prom: PromOverride;
  auto: boolean;
  // playbook: id of the playbook the operator picked, or undefined for
  // the profile's guided investigation flow.
  playbook?: string;
};

type Props = {
  onSubmit: (s: FormSubmission) => void;
};

export function InvestigationForm({ onSubmit }: Props) {
  const [schema, setSchema] = useState<InputSchema[] | null>(null);
  const [values, setValues] = useState<Record<string, Record<string, unknown>>>({});
  const [auto, setAuto] = useState(false);
  const [playbooks, setPlaybooks] = useState<PlaybookListItem[]>([]);
  const [playbookTypes, setPlaybookTypes] = useState<PlaybookTypeItem[]>([]);
  const [category, setCategory] = useState("");
  const [playbook, setPlaybook] = useState("");

  // Prom override panel state — unchanged from today.
  const [showPromOverrides, setShowPromOverrides] = useState(false);
  const [promService, setPromService] = useState("");
  const [promNamespace, setPromNamespace] = useState("");
  const [promPort, setPromPort] = useState("");
  const [promDisabled, setPromDisabled] = useState(false);

  useEffect(() => {
    api.getProfileInputs().then(setSchema).catch(() => setSchema([]));
    // The picker degrades to "default flow only" when the catalog
    // can't be fetched; the operator can still start a session.
    api
      .listPlaybooks()
      .then(setPlaybooks)
      .catch(() => setPlaybooks([]));
    // Type descriptions only decorate the category picker; a failed
    // fetch leaves the groups undescribed, not the picker empty.
    api
      .listPlaybookTypes()
      .then(setPlaybookTypes)
      .catch(() => setPlaybookTypes([]));
    // Pre-populate prom override fields with the profile's defaults so the
    // operator sees what's currently configured. Functional setters so a
    // late-arriving fetch can't overwrite values the operator typed while
    // the fetch was in flight — fields the operator left blank still get
    // the default. Failures (no profile, 503) leave the fields empty.
    api.getProfilePromDefaults().then((defaults) => {
      if (!defaults) return;
      if (defaults.service) setPromService((prev) => prev || defaults.service);
      if (defaults.namespace) setPromNamespace((prev) => prev || defaults.namespace);
      if (defaults.port > 0) setPromPort((prev) => prev || String(defaults.port));
    });
  }, []);

  if (!schema) {
    return (
      <div className="flex items-center gap-2 text-sm text-zinc-500">
        <Spinner /> loading form…
      </div>
    );
  }

  function submit(e: React.FormEvent) {
    e.preventDefault();
    const prom: PromOverride = { disabled: promDisabled };
    if (!promDisabled) {
      if (promService.trim()) prom.service = promService.trim();
      if (promNamespace.trim()) prom.namespace = promNamespace.trim();
      const portNum = parseInt(promPort, 10);
      if (!Number.isNaN(portNum) && portNum > 0) prom.port = portNum;
    }
    onSubmit({ inputs: values, prom, auto, playbook: playbook || undefined });
  }

  function setValue(id: string, next: Record<string, unknown>) {
    setValues((prev) => ({ ...prev, [id]: next }));
  }

  const groups = groupPlaybooks(playbooks, playbookTypes);
  const activeGroup = groups.find((g) => g.name === category);
  const visiblePlaybooks = activeGroup ? activeGroup.playbooks : groups.flatMap((g) => g.playbooks);
  const chosenPlaybook = visiblePlaybooks.find((p) => p.id === playbook);

  function pickCategory(next: string) {
    setCategory(next);
    const stillVisible = groups.find((g) => g.name === next)?.playbooks.some((p) => p.id === playbook);
    if (next && !stillVisible) setPlaybook("");
  }

  function pickPlaybook(next: string) {
    setPlaybook(next);
    const owner = groups.find((g) => g.playbooks.some((p) => p.id === next));
    if (owner) setCategory(owner.name);
  }

  return (
    <form onSubmit={submit} className="space-y-5">
      {schema.map((s) => {
        const v = values[s.id] ?? defaultValueForType(s.type);
        const onChange = (next: Record<string, unknown>) => setValue(s.id, next);
        switch (s.type) {
          case "text":
            return (
              <TextInput
                key={s.id}
                schema={s}
                value={v as { value: string }}
                onChange={onChange as (n: { value: string }) => void}
              />
            );
          case "url":
            return (
              <UrlInput
                key={s.id}
                schema={s}
                value={v as { value: string }}
                onChange={onChange as (n: { value: string }) => void}
              />
            );
          case "textarea":
            return (
              <TextareaInput
                key={s.id}
                schema={s}
                value={v as { value: string }}
                onChange={onChange as (n: { value: string }) => void}
              />
            );
          case "cluster_id":
            return (
              <ClusterIdInput
                key={s.id}
                schema={s}
                value={v as { value: string }}
                onChange={onChange as (n: { value: string }) => void}
              />
            );
          case "slack_channel":
            return (
              <SlackChannelInput
                key={s.id}
                schema={s}
                value={v as { id: string; name: string; url: string }}
                onChange={onChange as (n: { id: string; name: string; url: string }) => void}
              />
            );
        }
      })}

      {groups.length > 0 && (
        <div>
          <span className="font-medium">Playbook</span>
          <span className="block text-sm text-zinc-500">
            Walk a specific playbook instead of the guided investigation flow.
          </span>
          <div className="mt-2 grid max-w-2xl gap-3 sm:grid-cols-2">
            <label className="block min-w-0">
              <span className="block text-sm font-medium">Category</span>
              <select
                value={category}
                onChange={(e) => pickCategory(e.target.value)}
                className={inputClass + " mt-1"}
              >
                <option value="">All categories</option>
                {groups.map((g) => (
                  <option key={g.name} value={g.name}>
                    {g.name}
                  </option>
                ))}
              </select>
              {activeGroup?.description && (
                <span className="mt-1 block break-words text-sm text-zinc-500">
                  {activeGroup.description}
                </span>
              )}
            </label>
            <label className="block min-w-0">
              <span className="block text-sm font-medium">Playbook</span>
              <select
                value={playbook}
                onChange={(e) => pickPlaybook(e.target.value)}
                className={inputClass + " mt-1"}
              >
                <option value="">Guided investigation (default)</option>
                {visiblePlaybooks.map((p) => (
                  <option key={p.id} value={p.id} title={p.symptom || p.description}>
                    {p.id}
                  </option>
                ))}
              </select>
              {chosenPlaybook && (chosenPlaybook.symptom || chosenPlaybook.description) && (
                <span className="mt-1 block break-words text-sm text-zinc-500">
                  {chosenPlaybook.symptom || chosenPlaybook.description}
                </span>
              )}
            </label>
          </div>
        </div>
      )}

      <label className="flex items-start gap-2 cursor-pointer">
        <input
          type="checkbox"
          checked={auto}
          onChange={(e) => setAuto(e.target.checked)}
          className="mt-1"
        />
        <span>
          <span className="font-medium">Run in auto mode</span>
          <span className="block text-sm text-zinc-500">
            An operator agent will drive the investigation. You can take over at any time.
          </span>
        </span>
      </label>

      <details
        open={showPromOverrides}
        onToggle={(e) => setShowPromOverrides((e.target as HTMLDetailsElement).open)}
        className="rounded border border-zinc-800 bg-zinc-900/40"
      >
        <summary className="cursor-pointer px-3 py-2 text-sm text-zinc-300">
          Prometheus configuration
        </summary>
        <div className="space-y-4 border-t border-zinc-800 px-3 py-3">
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={promDisabled}
              onChange={(e) => setPromDisabled(e.target.checked)}
            />
            <span>Disable the prom MCP for this investigation</span>
          </label>

          <div className={promDisabled ? "opacity-50 pointer-events-none" : ""}>
            <Field label="service">
              <input
                value={promService}
                onChange={(e) => setPromService(e.target.value)}
                className={inputClass}
                placeholder="default-prometheus"
              />
            </Field>
            <Field label="namespace">
              <input
                value={promNamespace}
                onChange={(e) => setPromNamespace(e.target.value)}
                className={inputClass}
                placeholder="prometheus"
              />
            </Field>
            <Field label="port">
              <input
                value={promPort}
                onChange={(e) => setPromPort(e.target.value)}
                className={inputClass}
                placeholder="9090"
                inputMode="numeric"
              />
            </Field>
            <p className="text-xs text-zinc-500">
              Leave any field blank to use the launcher's default.
            </p>
          </div>
        </div>
      </details>

      <div className="space-y-2 pt-2">
        <div className="flex items-center justify-end">
          <button
            type="submit"
            className="inline-flex items-center gap-1.5 rounded bg-zinc-100 px-4 py-2 text-sm font-medium text-zinc-900 transition hover:bg-white"
          >
            run preflight
            <ArrowRightIcon className="h-4 w-4" />
          </button>
        </div>
      </div>
    </form>
  );
}

function defaultValueForType(type: InputSchema["type"]): Record<string, unknown> {
  if (type === "slack_channel") return { id: "", name: "", url: "" };
  return { value: "" };
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1 block text-xs uppercase tracking-wide text-zinc-400">
        {label}
      </span>
      {children}
    </label>
  );
}

const inputClass =
  "w-full rounded border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-100 placeholder-zinc-600 focus:border-zinc-600 focus:outline-none";
