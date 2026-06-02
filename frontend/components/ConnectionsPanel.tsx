"use client";

import { useEffect, useState } from "react";
import {
  api,
  ApiError,
  type CloudConnection,
  type ConnectionStatus,
} from "@/lib/api";
import { useDialog } from "@/lib/dialog";
import {
  AwsIcon,
  CheckIcon,
  CloudIcon,
  GcpIcon,
  IncidentIoIcon,
  SlackIcon,
} from "./Icons";
import { Spinner } from "./Spinner";

// ConnectionsPanel sits at the bottom of the sidenav next to
// LinkedReposPanel. The header strip is a single button: brand icons
// for Slack and incident.io with grayscale-when-not-connected as the
// status indicator. Clicking opens a modal where tokens get linked,
// replaced, or cleared — that flow is too tall for the strip itself.

type Props = {
  refreshNonce?: number;
};

export function ConnectionsPanel({ refreshNonce }: Props) {
  const [status, setStatus] = useState<ConnectionStatus | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [open, setOpen] = useState(false);

  function reload() {
    api
      .getConnections()
      .then((s) => {
        setStatus(s);
        setErr(null);
      })
      .catch((e) => setErr(e instanceof ApiError ? e.message : String(e)));
  }

  useEffect(() => {
    reload();
  }, [refreshNonce]);

  return (
    <div className="border-t border-zinc-800">
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="flex w-full items-center justify-between gap-2 px-3 py-2.5 text-left transition hover:bg-zinc-900/40"
        aria-label="manage connections"
      >
        <span className="text-xs uppercase tracking-wide text-zinc-500">
          connections
        </span>
        <span className="flex items-center gap-2">
          <BrandStatus
            connected={status?.slack ?? false}
            title={`Slack ${status?.slack ? "connected" : "not connected"}`}
          >
            <SlackIcon className="h-4 w-4" />
          </BrandStatus>
          <BrandStatus
            connected={status?.incidentio ?? false}
            title={`incident.io ${status?.incidentio ? "connected" : "not connected"}`}
          >
            <IncidentIoIcon className="h-4 w-4" />
          </BrandStatus>
        </span>
      </button>

      {open && (
        <ManageConnectionsModal
          onClose={() => setOpen(false)}
          status={status}
          loadErr={err}
          onChanged={reload}
        />
      )}
    </div>
  );
}

function BrandStatus({
  connected,
  title,
  children,
}: {
  connected: boolean;
  title: string;
  children: React.ReactNode;
}) {
  return (
    <span
      title={title}
      className={connected ? "" : "opacity-40 grayscale"}
      aria-label={title}
    >
      {children}
    </span>
  );
}

function ManageConnectionsModal({
  onClose,
  status,
  loadErr,
  onChanged,
}: {
  onClose: () => void;
  status: ConnectionStatus | null;
  loadErr: string | null;
  onChanged: () => void;
}) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4">
      <div className="max-h-[80vh] w-full max-w-lg overflow-y-auto rounded border border-zinc-800 bg-zinc-950 p-5 text-sm shadow-xl">
        <div className="mb-4 flex items-center justify-between">
          <h3 className="text-base font-medium text-zinc-100">Connections</h3>
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
          Tokens are validated against the upstream service before being
          saved locally under{" "}
          <code className="font-mono text-zinc-400">
            ~/.config/triagent/credentials.json
          </code>
          .
        </p>

        {loadErr && (
          <div className="mb-3 rounded border border-red-900/60 bg-red-950/40 px-2 py-1.5 text-xs text-red-200/80">
            {loadErr}
          </div>
        )}

        <div className="space-y-3">
          <ConnectionCard
            label="Slack"
            icon={<SlackIcon className="h-5 w-5" />}
            connected={status?.slack ?? false}
            placeholder="xoxp-…"
            hint={
              <>
                Create a Slack app at{" "}
                <a
                  href="https://api.slack.com/apps"
                  target="_blank"
                  rel="noreferrer"
                  className="text-zinc-300 underline decoration-zinc-700 underline-offset-2 hover:decoration-zinc-400"
                >
                  api.slack.com/apps
                </a>{" "}
                (or ask your Slack admin for an existing one), grant{" "}
                <code className="font-mono">channels:history</code>,{" "}
                <code className="font-mono">channels:read</code>, and{" "}
                <code className="font-mono">search:read</code> user-token
                scopes, then copy your{" "}
                <code className="font-mono">xoxp-</code> user OAuth token from{" "}
                <em>OAuth &amp; Permissions</em>.
              </>
            }
            onSave={(token) => api.putSlackToken(token)}
            onClear={() => api.clearConnection("slack")}
            onChanged={onChanged}
          />
          <ConnectionCard
            label="incident.io"
            icon={<IncidentIoIcon className="h-5 w-5" />}
            connected={status?.incidentio ?? false}
            placeholder="paste your API key"
            hint={
              <>
                Generate an API key at{" "}
                <a
                  href="https://app.incident.io/settings/api-keys"
                  target="_blank"
                  rel="noreferrer"
                  className="text-zinc-300 underline decoration-zinc-700 underline-offset-2 hover:decoration-zinc-400"
                >
                  incident.io → Settings → API keys
                </a>
                . The key needs read access to incidents and post-mortems.
              </>
            }
            onSave={(token) => api.putIncidentioToken(token)}
            onClear={() => api.clearConnection("incidentio")}
            onChanged={onChanged}
          />
        </div>

        <CloudConnectionsSection cloud={status?.cloud ?? []} />
      </div>
    </div>
  );
}

// CloudConnectionsSection renders the profile-configured cloud connections as
// read-only pills: the assumed identity with a checkmark when the probe is
// valid, the reauth hint when not. Cloud is configured in the profile, never
// entered here — these pills carry no edit affordance. Omitted entirely when no
// cloud sources are configured.
function CloudConnectionsSection({ cloud }: { cloud: CloudConnection[] }) {
  if (cloud.length === 0) return null;
  return (
    <div className="mt-4" data-testid="cloud-connections">
      <div className="mb-2 flex items-center gap-2 text-xs uppercase tracking-wide text-zinc-500">
        <CloudIcon className="h-3.5 w-3.5" />
        cloud
      </div>
      <p className="mb-2 text-xs text-zinc-500">
        Read-only identities pinned in the profile&rsquo;s{" "}
        <code className="font-mono text-zinc-400">cloud:</code> block, not edited
        here. See the Cloud providers docs for setup.
      </p>
      <div className="space-y-2">
        {cloud.map((c) => (
          <CloudPill key={c.alias} conn={c} />
        ))}
      </div>
    </div>
  );
}

// ProviderMark renders the cloud provider's brand icon, falling back to the
// generic cloud glyph for an unrecognised provider. The provider name stays in
// the title/aria-label so it is still announced and discoverable.
function ProviderMark({ provider }: { provider: string }) {
  const title = provider.toUpperCase();
  const icon =
    provider === "aws" ? (
      <AwsIcon className="h-4 w-4" />
    ) : provider === "gcp" ? (
      <GcpIcon className="h-4 w-4" />
    ) : (
      <CloudIcon className="h-4 w-4 text-zinc-500" />
    );
  return (
    <span title={title} aria-label={title} className="shrink-0">
      {icon}
    </span>
  );
}

function CloudPill({ conn }: { conn: CloudConnection }) {
  return (
    <div
      data-cloud-pill
      className="rounded border border-zinc-800 bg-zinc-900/40 p-3"
    >
      <div className="flex items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2">
          <ProviderMark provider={conn.provider} />
          <span className="truncate text-xs font-medium text-zinc-200">
            {conn.alias}
          </span>
        </div>
        {conn.valid ? (
          <span
            className="flex items-center gap-1 text-[10px] uppercase tracking-wide text-emerald-300/80"
            title="identity valid"
          >
            <CheckIcon className="h-3.5 w-3.5" />
            valid
          </span>
        ) : (
          <span className="text-[10px] uppercase tracking-wide text-amber-300/80">
            unavailable
          </span>
        )}
      </div>
      <CloudPillBody conn={conn} />
      {!conn.valid && (
        <div className="mt-1 text-xs text-amber-200/70">
          {conn.hint ?? reauthHint(conn.provider)}
        </div>
      )}
    </div>
  );
}

// CloudPillBody renders the same two-line shape for both providers — the
// principal over the reach it grants — with provider-specific content. GCP: the
// impersonated service account over its allowlisted project count. AWS: the SSO
// base profile over its account count. The reach line's full members are in its
// hover title.
function CloudPillBody({ conn }: { conn: CloudConnection }) {
  const { principal, reach, reachTitle } =
    conn.provider === "aws"
      ? {
          principal: conn.source_profile ? `base: ${conn.source_profile}` : "",
          reach: countLabel(conn.accounts?.length ?? 0, "account"),
          reachTitle: (conn.accounts ?? []).join(", "),
        }
      : {
          principal: conn.assumed_identity ?? "",
          // An empty projects allowlist means the identity reaches any project
          // its IAM grants, not zero.
          reach:
            (conn.projects?.length ?? 0) === 0
              ? "all projects"
              : countLabel(conn.projects!.length, "project"),
          reachTitle: (conn.projects ?? []).join(", "),
        };
  return (
    <>
      <div className="mt-1 truncate font-mono text-xs text-zinc-400">
        {principal}
      </div>
      <div className="truncate text-[10px] text-zinc-500" title={reachTitle}>
        {reach}
      </div>
    </>
  );
}

function countLabel(n: number, noun: string): string {
  return `${n} ${noun}${n === 1 ? "" : "s"}`;
}

// reauthHint is the operator's own re-login command for a stale cloud identity,
// shown on an invalid pill when the probe did not supply a more specific hint.
function reauthHint(provider: string): string {
  if (provider === "gcp") return "Re-authenticate with: gcloud auth login";
  if (provider === "aws") return "Re-authenticate with: aws sso login";
  return "Re-authenticate through your own cloud login";
}

type ConnectionCardProps = {
  label: string;
  icon: React.ReactNode;
  connected: boolean;
  placeholder: string;
  hint: React.ReactNode;
  onSave: (token: string) => Promise<unknown>;
  onClear: () => Promise<unknown>;
  onChanged: () => void;
};

function ConnectionCard({
  label,
  icon,
  connected,
  placeholder,
  hint,
  onSave,
  onClear,
  onChanged,
}: ConnectionCardProps) {
  const [editing, setEditing] = useState(false);
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [actionErr, setActionErr] = useState<string | null>(null);
  const dialog = useDialog();

  useEffect(() => {
    if (!editing) {
      setToken("");
      setActionErr(null);
    }
  }, [editing]);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    if (!token.trim()) return;
    setBusy(true);
    setActionErr(null);
    try {
      await onSave(token.trim());
      setEditing(false);
      onChanged();
    } catch (err) {
      setActionErr(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function clear() {
    const ok = await dialog.confirm({
      title: `Disconnect ${label}?`,
      body: "Future sessions will not be able to ingest from this provider until you re-link a token.",
      confirmLabel: "Disconnect",
      danger: true,
    });
    if (!ok) return;
    setBusy(true);
    setActionErr(null);
    try {
      await onClear();
      onChanged();
    } catch (err) {
      setActionErr(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="rounded border border-zinc-800 bg-zinc-900/40 p-3">
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <span className={connected ? "" : "opacity-40 grayscale"}>
            {icon}
          </span>
          <div className="font-medium text-zinc-200">{label}</div>
        </div>
        {connected ? (
          <span className="text-[10px] uppercase tracking-wide text-emerald-300/80">
            connected
          </span>
        ) : (
          <span className="text-[10px] uppercase tracking-wide text-zinc-500">
            not connected
          </span>
        )}
      </div>
      {!editing && (
        <div className="mt-2 flex items-center justify-between gap-2 text-xs text-zinc-500">
          <span>{connected ? "token on file (or via env)" : "no token yet"}</span>
          <span className="flex gap-2">
            <button
              type="button"
              onClick={() => setEditing(true)}
              className="rounded text-zinc-400 transition hover:text-zinc-200"
            >
              {connected ? "replace" : "link"}
            </button>
            {connected && (
              <button
                type="button"
                onClick={clear}
                disabled={busy}
                className="rounded text-zinc-500 transition hover:text-red-400 disabled:opacity-40"
              >
                disconnect
              </button>
            )}
          </span>
        </div>
      )}
      {editing && (
        <form onSubmit={save} className="mt-2 space-y-2">
          <div className="text-xs leading-relaxed text-zinc-500">{hint}</div>
          <input
            type="password"
            placeholder={placeholder}
            value={token}
            onChange={(e) => setToken(e.target.value)}
            className="w-full rounded border border-zinc-800 bg-zinc-950 px-2 py-1 text-xs font-mono text-zinc-100 focus:border-zinc-600 focus:outline-none"
            required
            autoFocus
          />
          <div className="flex items-center justify-between">
            {actionErr ? (
              <span className="text-xs text-red-400">{actionErr}</span>
            ) : (
              <span className="text-xs text-zinc-600">
                token is validated against the upstream service before saving
              </span>
            )}
            <div className="flex gap-2">
              <button
                type="button"
                onClick={() => setEditing(false)}
                disabled={busy}
                className="rounded text-xs text-zinc-500 transition hover:text-zinc-300 disabled:opacity-40"
              >
                cancel
              </button>
              <button
                type="submit"
                disabled={busy || !token.trim()}
                className="inline-flex items-center gap-1 rounded bg-zinc-100 px-2.5 py-1 text-xs font-medium text-zinc-900 transition hover:bg-white disabled:cursor-not-allowed disabled:bg-zinc-700 disabled:text-zinc-400"
              >
                {busy && <Spinner className="h-3 w-3" />}
                save
              </button>
            </div>
          </div>
        </form>
      )}
    </div>
  );
}
