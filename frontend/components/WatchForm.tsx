"use client";

import { useEffect, useState } from "react";
import { api, type ConnectionStatus, type Watch, type WatchFilter, type WatchSourceKind } from "@/lib/api";
import { formatDuration, parseDuration } from "@/lib/duration";
import { FilterBuilder } from "./FilterBuilder";
import { SlackChannelPicker } from "./SlackChannelPicker";

type FormShape = Omit<Watch, "id" | "createdAt">;

export function WatchForm({
  initial,
  onSubmit,
  submitLabel = "Create watch",
}: {
  initial?: FormShape;
  onSubmit: (w: FormShape) => void;
  submitLabel?: string;
}) {
  const [name, setName] = useState(initial?.name ?? "");
  const [description, setDescription] = useState(initial?.description ?? "");
  const [kind, setKind] = useState<WatchSourceKind>(initial?.source.kind ?? "github_issues");
  const [owner, setOwner] = useState(initial?.source.owner ?? "");
  const [repo, setRepo] = useState(initial?.source.repo ?? "");
  const [labels, setLabels] = useState(initial?.source.labels?.join(",") ?? "");
  const [states, setStates] = useState(initial?.source.states?.join(",") ?? "open");
  const [channelID, setChannelID] = useState(initial?.source.channelID ?? "");
  const [channelName, setChannelName] = useState(initial?.source.channelName ?? "");
  const [includeThreadReplies, setIncludeThreadReplies] = useState(initial?.source.includeThreadReplies ?? false);
  const [intervalSeconds, setIntervalSeconds] = useState(initial?.polling.intervalSeconds ?? 300);
  const [customInstructions, setCustomInstructions] = useState(initial?.ingest?.customInstructions ?? "");
  // autoIngest gates the ingestion agent run; autoStart gates whether
  // the agent's investigation_started suggestions auto-spawn. Existing
  // watches saved before this split implicitly had ingest tied to
  // auto-start, so an old autoStart=true watch defaults autoIngest on.
  const [autoIngest, setAutoIngest] = useState(initial?.ingest?.enabled ?? initial?.autoStart?.enabled ?? false);
  const [autoStart, setAutoStart] = useState(initial?.autoStart?.enabled ?? false);
  const [maxConcurrent, setMaxConcurrent] = useState(initial?.autoStart?.maxConcurrent ?? 1);
  const [filters, setFilters] = useState<WatchFilter[]>(initial?.source.filters ?? []);
  const [conn, setConn] = useState<ConnectionStatus | null>(null);

  useEffect(() => {
    api
      .getConnections()
      .then(setConn)
      .catch(() => setConn({ slack: false, incidentio: false, slack_channel_prefix: "" }));
  }, []);

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    const body: FormShape = {
      name,
      description: description.trim() || undefined,
      source: kind === "github_issues"
        ? { kind, owner, repo, labels: labels.split(",").map(s => s.trim()).filter(Boolean), states: states.split(",").map(s => s.trim()).filter(Boolean), filters }
        : { kind, channelID, channelName, includeThreadReplies, filters },
      polling: { intervalSeconds },
      ingest: { enabled: autoIngest, customInstructions },
      // autoStart implies autoIngest — coerce here so the backend
      // never sees the invalid combination autoStart=on/ingest=off.
      autoStart: { enabled: autoStart && autoIngest, maxConcurrent },
      enabled: true,
    };
    onSubmit(body);
  }

  return (
    <form onSubmit={handleSubmit} className="max-w-5xl space-y-4">
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {/* Left column — Source: what the watch polls. */}
        <section className="space-y-5 rounded border border-zinc-800 bg-zinc-900/40 p-4">
          <div className="text-xs uppercase tracking-wide text-zinc-500">Source</div>

          <Field label="Name">
            <input
              id="watch-name"
              value={name}
              onChange={e => setName(e.target.value)}
              required
              className={inputClass}
              placeholder="My watch"
              aria-label="name"
            />
          </Field>

          <Field label="Description (optional)">
            <input
              id="watch-description"
              value={description}
              onChange={e => setDescription(e.target.value)}
              className={inputClass}
              placeholder="One-line context — shown on the watches overview"
              aria-label="description"
            />
          </Field>

          <div>
            <span className="mb-2 block text-xs uppercase tracking-wide text-zinc-400">Source kind</span>
            <div className="flex gap-4">
              <label className="flex cursor-pointer items-center gap-2 text-sm text-zinc-300">
                <input
                  type="radio"
                  name="kind"
                  checked={kind === "github_issues"}
                  onChange={() => setKind("github_issues")}
                  className="accent-zinc-400"
                  aria-label="GitHub Issues"
                />
                GitHub issues
              </label>
              <label
                className={`flex items-center gap-2 text-sm ${conn?.slack ? "cursor-pointer text-zinc-300" : "cursor-not-allowed text-zinc-600"}`}
                title={conn?.slack ? undefined : "Link a Slack token in the Connections panel to enable this source"}
              >
                <input
                  type="radio"
                  name="kind"
                  checked={kind === "slack_channel"}
                  onChange={() => setKind("slack_channel")}
                  disabled={!conn?.slack}
                  className="accent-zinc-400"
                  aria-label="Slack channel"
                />
                Slack channel
                {!conn?.slack && (
                  <span className="text-xs text-zinc-500">(connect Slack to enable)</span>
                )}
              </label>
            </div>
          </div>

          {kind === "github_issues" ? (
            <>
              <div className="grid grid-cols-2 gap-3">
                <Field label="Owner">
                  <input
                    id="watch-owner"
                    value={owner}
                    onChange={e => setOwner(e.target.value)}
                    required
                    className={inputClass}
                    placeholder="my-org"
                    aria-label="owner"
                  />
                </Field>
                <Field label="Repo">
                  <input
                    id="watch-repo"
                    value={repo}
                    onChange={e => setRepo(e.target.value)}
                    required
                    className={inputClass}
                    placeholder="c1"
                    aria-label="repo"
                  />
                </Field>
              </div>
              <Field label="Labels (comma-separated, AND)">
                <input
                  id="watch-labels"
                  value={labels}
                  onChange={e => setLabels(e.target.value)}
                  className={inputClass}
                  placeholder="bug, triage"
                  aria-label="labels"
                />
              </Field>
              <Field label="States">
                <input
                  id="watch-states"
                  value={states}
                  onChange={e => setStates(e.target.value)}
                  className={inputClass}
                  placeholder="open"
                  aria-label="states"
                />
              </Field>
            </>
          ) : (
            <>
              {conn?.slack ? (
                <Field label="Channel">
                  <SlackChannelPicker
                    value={channelID}
                    onChange={(id, ch) => {
                      setChannelID(id);
                      setChannelName(ch?.name ?? "");
                    }}
                  />
                </Field>
              ) : (
                <>
                  <Field label="Channel ID">
                    <input
                      id="watch-channel-id"
                      value={channelID}
                      onChange={e => setChannelID(e.target.value)}
                      required
                      className={inputClass}
                      placeholder="C0123ABCDEF"
                      aria-label="channel id"
                    />
                  </Field>
                  <Field label="Channel name (display)">
                    <input
                      id="watch-channel-name"
                      value={channelName}
                      onChange={e => setChannelName(e.target.value)}
                      className={inputClass}
                      placeholder="alerts-prod"
                      aria-label="channel name"
                    />
                  </Field>
                  <p className="text-xs text-zinc-500">
                    Slack isn&apos;t connected — paste the channel ID or wire a token in the Connections panel for the picker.
                  </p>
                </>
              )}
              <label className="flex cursor-pointer items-center gap-2 text-sm text-zinc-300">
                <input
                  id="watch-thread-replies"
                  type="checkbox"
                  checked={includeThreadReplies}
                  onChange={e => setIncludeThreadReplies(e.target.checked)}
                  className="accent-zinc-400"
                />
                Include thread replies
              </label>
            </>
          )}

          <div>
            <span className="mb-2 block text-xs uppercase tracking-wide text-zinc-400">Filters</span>
            <FilterBuilder kind={kind} value={filters} onChange={setFilters} />
          </div>
        </section>

        {/* Right column — Behavior: how often to poll and what to do with results. */}
        <section className="space-y-5 rounded border border-zinc-800 bg-zinc-900/40 p-4">
          <div className="text-xs uppercase tracking-wide text-zinc-500">Behavior</div>

          <Field label="Polling interval">
            <DurationField
              valueSeconds={intervalSeconds}
              onChange={setIntervalSeconds}
              minSeconds={300}
              maxSeconds={86400}
            />
          </Field>

          <Field label="Custom instructions (optional, ≤4096 chars)">
            <textarea
              id="watch-instructions"
              value={customInstructions}
              maxLength={4096}
              onChange={e => setCustomInstructions(e.target.value)}
              className={`${inputClass} h-24 resize-y`}
              placeholder="Tell the ingestion agent what to look for…"
              aria-label="custom instructions"
            />
          </Field>

          <div className="space-y-3 rounded border border-zinc-800 bg-zinc-950/60 p-3">
            <label className="flex cursor-pointer items-start gap-2 text-sm text-zinc-300">
              <input
                id="watch-autoingest"
                type="checkbox"
                checked={autoIngest}
                onChange={e => {
                  setAutoIngest(e.target.checked);
                  if (!e.target.checked) setAutoStart(false);
                }}
                className="mt-0.5 accent-zinc-400"
              />
              <span>
                <span className="font-medium">Auto-ingest signals</span>
                <span className="mt-0.5 block text-xs text-zinc-500">
                  Run the ingestion agent on every poll to classify items into investigation-worthy / unclear / dismissed signals with briefings and reasons. Without this, signals are 1:1 with items.
                </span>
              </span>
            </label>

            <label
              className={`flex items-start gap-2 text-sm ${autoIngest ? "cursor-pointer text-zinc-300" : "cursor-not-allowed text-zinc-600"}`}
              title={autoIngest ? undefined : "Enable Auto-ingest first — auto-start operates on the agent's signals."}
            >
              <input
                id="watch-autostart"
                type="checkbox"
                checked={autoStart && autoIngest}
                disabled={!autoIngest}
                onChange={e => setAutoStart(e.target.checked)}
                className="mt-0.5 accent-zinc-400"
              />
              <span>
                <span className="font-medium">Auto-start investigations</span>
                <span className="mt-0.5 block text-xs text-zinc-500">
                  Automatically spawn an investigation for each signal the agent classifies as investigation-worthy. Without this, the agent's recommendations land as <span className="text-zinc-400">proposed</span> signals — the operator clicks Start to launch.
                </span>
              </span>
            </label>
            {autoStart && autoIngest && (
              <Field label="Max concurrent">
                <input
                  id="watch-max-concurrent"
                  type="number"
                  min={1}
                  max={10}
                  value={maxConcurrent}
                  onChange={e => setMaxConcurrent(Number(e.target.value))}
                  className="w-24 rounded border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-100 focus:border-zinc-600 focus:outline-none"
                  aria-label="max concurrent"
                />
              </Field>
            )}
          </div>
        </section>
      </div>

      <div className="flex items-center justify-end pt-2">
        <button
          type="submit"
          className="inline-flex items-center gap-1.5 rounded bg-zinc-100 px-4 py-2 text-sm font-medium text-zinc-900 transition hover:bg-white disabled:cursor-not-allowed disabled:bg-zinc-700 disabled:text-zinc-400"
        >
          {submitLabel}
        </button>
      </div>
    </form>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1 block text-xs uppercase tracking-wide text-zinc-400">{label}</span>
      {children}
    </label>
  );
}

// DurationField accepts human-readable interval strings ("5m", "1h",
// "1h30m") AND bare seconds for back-compat. Validates against the
// caller's min/max on blur or Enter and surfaces an inline error if
// the input falls outside the range or can't be parsed.
function DurationField({
  valueSeconds,
  onChange,
  minSeconds,
  maxSeconds,
}: {
  valueSeconds: number;
  onChange: (seconds: number) => void;
  minSeconds: number;
  maxSeconds: number;
}) {
  const [draft, setDraft] = useState(formatDuration(valueSeconds));
  const [error, setError] = useState<string | null>(null);

  // Keep the draft in sync if the parent re-initialises (e.g. edit
  // page loading from the API).
  useEffect(() => {
    setDraft(formatDuration(valueSeconds));
  }, [valueSeconds]);

  function commit() {
    const parsed = parseDuration(draft);
    if (parsed == null) {
      setError("Use forms like 5m, 1h, 1h30m, or a number of seconds.");
      return;
    }
    if (parsed < minSeconds || parsed > maxSeconds) {
      setError(`Must be between ${formatDuration(minSeconds)} and ${formatDuration(maxSeconds)}.`);
      return;
    }
    setError(null);
    onChange(parsed);
    setDraft(formatDuration(parsed));
  }

  return (
    <div className="space-y-1">
      <div className="flex items-center gap-3">
        <input
          id="watch-interval"
          type="text"
          value={draft}
          onChange={e => {
            setDraft(e.target.value);
            if (error) setError(null);
          }}
          onBlur={commit}
          onKeyDown={e => {
            if (e.key === "Enter") {
              e.preventDefault();
              commit();
            }
          }}
          className="w-32 rounded border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-100 focus:border-zinc-600 focus:outline-none"
          placeholder="5m"
          aria-label="polling interval"
        />
        <span className="text-xs text-zinc-500">
          {formatDuration(minSeconds)} – {formatDuration(maxSeconds)} (e.g. 5m, 1h30m)
        </span>
      </div>
      {error && <p className="text-xs text-red-400">{error}</p>}
    </div>
  );
}

const inputClass =
  "w-full rounded border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-100 placeholder-zinc-600 focus:border-zinc-600 focus:outline-none";
