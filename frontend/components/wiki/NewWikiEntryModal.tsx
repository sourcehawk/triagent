"use client";

import { useEffect, useMemo, useState } from "react";
import {
  api,
  ApiError,
  type ConnectionStatus,
  type EditorSession,
  type EditorSources,
  type Investigation,
} from "@/lib/api";
import { Spinner } from "@/components/Spinner";
import { SlackChannelPicker } from "@/components/SlackChannelPicker";
import { sinceISOFromCreatedUnix } from "@/lib/slackChannels";

// NewWikiEntryModal kicks off a wiki entry the operator wants the agent
// to draft. The operator types a wiki entry slug (free-form lowercase-
// with-hyphens) and optionally attaches any combination of three
// sources: a local investigation session, an incident.io ticket, or a
// Slack channel. All three are optional — capture from scratch is a
// valid flow. The backend creates an editor session with subject =
// wiki:entry:<slug>, attaches the chosen sources, spawns the triagent-slack /
// triagent-incidentio MCP servers when their tokens are linked, and returns
// the session DTO. The caller routes the operator to the wiki editor
// with `?session=<id>` so the chat drawer mounts open.

type Props = {
  open: boolean;
  onClose: () => void;
  // onCreated runs after the editor session is created. The caller
  // typically navigates to the wiki editor with the new session id so
  // the chat drawer opens straight away.
  onCreated: (session: EditorSession, slug: string) => void;
};

const DEFAULT_DAYS_AGO = 7;

function defaultSinceISO(): string {
  const d = new Date();
  d.setDate(d.getDate() - DEFAULT_DAYS_AGO);
  // datetime-local wants YYYY-MM-DDTHH:MM, no seconds, no Z.
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// investigationLabel picks the most human-readable label for an
// investigation in a picker option. Falls back through the chain
// (operator-supplied label → namespace → short id) so even unlabelled
// sessions are identifiable.
function investigationLabel(inv: Investigation): string {
  if (inv.label && inv.label.trim() !== "") return inv.label;
  const ns = inv.namespace?.trim();
  if (ns) return ns;
  return inv.id.slice(0, 8);
}

export function NewWikiEntryModal({ open, onClose, onCreated }: Props) {
  const [conn, setConn] = useState<ConnectionStatus | null>(null);

  const [slug, setSlug] = useState("");
  const [useInvestigation, setUseInvestigation] = useState(false);
  const [useIncidentio, setUseIncidentio] = useState(false);
  const [useSlack, setUseSlack] = useState(false);
  const [investigationID, setInvestigationID] = useState("");
  const [investigations, setInvestigations] = useState<Investigation[]>([]);
  const [investigationsLoading, setInvestigationsLoading] = useState(false);
  const [investigationsErr, setInvestigationsErr] = useState<string | null>(
    null,
  );
  const [incidentioURL, setIncidentioURL] = useState("");
  const [slackChannelID, setSlackChannelID] = useState("");
  const [slackChannelName, setSlackChannelName] = useState("");
  const [since, setSince] = useState(defaultSinceISO());

  const [submitting, setSubmitting] = useState(false);
  const [submitErr, setSubmitErr] = useState<string | null>(null);

  // Load connection status when the modal opens so we can disable the
  // source toggles for unconfigured providers.
  useEffect(() => {
    if (!open) return;
    setConn(null);
    api
      .getConnections()
      .then(setConn)
      .catch(() => setConn({ slack: false, incidentio: false, slack_channel_prefix: "" }));
  }, [open]);

  // Load the investigation list when the modal opens so the picker
  // has options ready by the time the operator ticks the toggle.
  useEffect(() => {
    if (!open) return;
    setInvestigationsLoading(true);
    setInvestigationsErr(null);
    api
      .listInvestigations()
      .then((list) => {
        setInvestigations(list);
        setInvestigationsLoading(false);
      })
      .catch((err) => {
        setInvestigationsErr(
          err instanceof ApiError ? err.message : String(err),
        );
        setInvestigationsLoading(false);
      });
  }, [open]);

  const validSlug = useMemo(() => {
    // Match the wiki schema's slug shape (lowercase-with-hyphens). We
    // don't enforce here — the backend validates — but greying out the
    // submit button on obvious malformed inputs reduces round-trips.
    return /^[a-z0-9][a-z0-9-]*$/.test(slug.trim());
  }, [slug]);

  const canSubmit = validSlug && !submitting;

  if (!open) return null;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!canSubmit) return;
    setSubmitting(true);
    setSubmitErr(null);
    try {
      const sources: EditorSources = {};
      if (useInvestigation) {
        if (!investigationID) {
          throw new Error("pick an investigation");
        }
        sources.investigationID = investigationID;
      }
      if (useIncidentio) {
        if (!incidentioURL.trim()) {
          throw new Error("incident.io URL is required when the toggle is on");
        }
        sources.incidentioURL = incidentioURL.trim();
      }
      if (useSlack) {
        if (!slackChannelID) {
          throw new Error("pick a Slack channel");
        }
        sources.slackChannelID = slackChannelID;
        sources.slackChannelName = slackChannelName;
        const t = new Date(since).getTime();
        if (Number.isFinite(t)) {
          sources.slackSinceUnix = Math.floor(t / 1000);
        }
      }
      const session = await api.createOrResumeEditorSession(
        { kind: "wiki", wiki: { kind: "entry", id: slug.trim() } },
        sources,
      );
      onCreated(session, slug.trim());
    } catch (err) {
      setSubmitErr(err instanceof ApiError ? err.message : String(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4">
      <div className="max-h-[90vh] w-full max-w-lg overflow-y-auto rounded border border-zinc-800 bg-zinc-950 p-5 text-sm shadow-xl">
        <div className="mb-4 flex items-center justify-between">
          <h3 className="text-base font-medium text-zinc-100">
            New wiki entry
          </h3>
          <button
            type="button"
            onClick={onClose}
            className="text-zinc-500 transition hover:text-zinc-200"
            aria-label="close"
          >
            ✕
          </button>
        </div>

        <p className="mb-4 text-xs text-zinc-500">
          Capture a learning into the wiki. Attach any combination of
          sources — an investigation session, an incident.io ticket, a Slack
          thread — and the agent reads them to draft the entry. All three are
          optional; you can also start blank and dictate the content.
        </p>

        <form onSubmit={submit} className="space-y-4">
          <Field
            label="entry slug"
            hint={
              <>
                Format:{" "}
                <code className="font-mono text-zinc-300">
                  ^[a-z0-9][a-z0-9-]*$
                </code>{" "}
                — lowercase-with-hyphens. Prefix with{" "}
                <code className="font-mono text-zinc-300">inc-</code> when
                anchored on an incident.io ticket,{" "}
                <code className="font-mono text-zinc-300">inv-</code> for an
                investigation session, or{" "}
                <code className="font-mono text-zinc-300">alert-</code> for a
                Slack alert thread. This is the wiki entry's filename + URL
                segment.
              </>
            }
          >
            <input
              type="text"
              value={slug}
              onChange={(e) => setSlug(e.target.value)}
              required
              placeholder="inc-zeebe-broker-oom"
              className="w-full rounded border border-zinc-800 bg-zinc-900 px-2 py-1.5 text-xs font-mono text-zinc-100 focus:border-zinc-600 focus:outline-none"
            />
            {!validSlug && slug.trim() !== "" && (
              <p className="mt-1 text-[11px] text-amber-400">
                doesn't match the expected format yet
              </p>
            )}
          </Field>

          <SourceToggle
            label="Attach an investigation session"
            checked={useInvestigation}
            onChange={setUseInvestigation}
            disabled={
              !investigationsLoading &&
              !investigationsErr &&
              investigations.length === 0
            }
            disabledReason={
              <span>
                no investigation sessions found on this launcher yet — start
                one from the homepage first.
              </span>
            }
          >
            {investigationsLoading ? (
              <div className="flex items-center gap-2 text-[11px] text-zinc-500">
                <Spinner className="h-3 w-3" /> loading investigations…
              </div>
            ) : investigationsErr ? (
              <p className="text-[11px] text-red-300">
                failed to list investigations: {investigationsErr}
              </p>
            ) : (
              <div>
                <select
                  value={investigationID}
                  onChange={(e) => setInvestigationID(e.target.value)}
                  required={useInvestigation}
                  className="w-full rounded border border-zinc-800 bg-zinc-900 px-2 py-1.5 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
                >
                  <option value="">— pick an investigation —</option>
                  {investigations.map((inv) => (
                    <option key={inv.id} value={inv.id}>
                      {investigationLabel(inv)} · {inv.id.slice(0, 8)}
                    </option>
                  ))}
                </select>
                <p className="mt-1 text-[11px] text-zinc-500">
                  The agent reads{" "}
                  <code className="font-mono text-zinc-300">events.jsonl</code>{" "}
                  from the picked session as primary evidence.
                </p>
              </div>
            )}
          </SourceToggle>

          <SourceToggle
            label="Pull context from incident.io"
            checked={useIncidentio}
            onChange={setUseIncidentio}
            disabled={!conn?.incidentio}
            disabledReason={
              <span>
                incident.io not connected — link an API key in the Connections
                panel first.
              </span>
            }
          >
            <input
              type="url"
              value={incidentioURL}
              onChange={(e) => setIncidentioURL(e.target.value)}
              required={useIncidentio}
              placeholder="https://app.incident.io/<org>/incidents/<n>"
              className="w-full rounded border border-zinc-800 bg-zinc-900 px-2 py-1.5 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
            />
          </SourceToggle>

          <SourceToggle
            label="Pull context from Slack channel"
            checked={useSlack}
            onChange={setUseSlack}
            disabled={!conn?.slack}
            disabledReason={
              <span>
                Slack not connected — link a token in the Connections panel
                first.
              </span>
            }
          >
            <div className="space-y-2">
              <SlackChannelPicker
                initialFilter={conn?.slack_channel_prefix ?? ""}
                value={slackChannelID}
                onChange={(id, ch) => {
                  setSlackChannelID(id);
                  setSlackChannelName(ch?.name ?? "");
                  // Every pick rewrites since to the channel's creation time when
                  // available, so re-picking a different channel gets the new default.
                  const iso = ch?.created_unix
                    ? sinceISOFromCreatedUnix(ch.created_unix)
                    : "";
                  if (iso) setSince(iso);
                }}
              />
              <div>
                <label className="mb-1 block text-[11px] uppercase tracking-wide text-zinc-500">
                  since
                </label>
                <input
                  type="datetime-local"
                  value={since}
                  onChange={(e) => setSince(e.target.value)}
                  className="w-full rounded border border-zinc-800 bg-zinc-900 px-2 py-1.5 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
                />
                <p className="mt-1 text-[11px] text-zinc-500">
                  Defaults to the channel's creation time when picked. Widen for long-lived channels (the slack MCP caps at the materialisation cap).
                </p>
              </div>
            </div>
          </SourceToggle>

          {submitErr && (
            <div className="rounded border border-red-900/60 bg-red-950/40 px-2 py-1.5 text-xs text-red-200/80">
              {submitErr}
            </div>
          )}

          <div className="flex items-center justify-between pt-2">
            <button
              type="button"
              onClick={onClose}
              className="text-xs text-zinc-500 transition hover:text-zinc-300"
            >
              cancel
            </button>
            <button
              type="submit"
              disabled={!canSubmit}
              className="inline-flex items-center gap-1.5 rounded bg-zinc-100 px-3 py-1.5 text-xs font-medium text-zinc-900 transition hover:bg-white disabled:cursor-not-allowed disabled:bg-zinc-700 disabled:text-zinc-400"
            >
              {submitting && <Spinner className="h-3 w-3" />}
              create &amp; open chat
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <label className="block">
      <span className="mb-1 block text-xs uppercase tracking-wide text-zinc-400">
        {label}
      </span>
      {children}
      {hint && <p className="mt-1 text-[11px] text-zinc-500">{hint}</p>}
    </label>
  );
}

function SourceToggle({
  label,
  checked,
  onChange,
  disabled,
  disabledReason,
  children,
}: {
  label: string;
  checked: boolean;
  onChange: (v: boolean) => void;
  disabled: boolean;
  disabledReason: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div className="rounded border border-zinc-800 bg-zinc-900/40 p-3">
      <label className="flex cursor-pointer items-center gap-2 text-zinc-200">
        <input
          type="checkbox"
          checked={checked}
          onChange={(e) => onChange(e.target.checked)}
          disabled={disabled}
        />
        <span className={disabled ? "text-zinc-500" : ""}>{label}</span>
      </label>
      {disabled && (
        <p className="mt-1 text-[11px] text-amber-400">{disabledReason}</p>
      )}
      {checked && !disabled && <div className="mt-2">{children}</div>}
    </div>
  );
}
