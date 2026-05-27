"use client";

import { useEffect, useState } from "react";
import { api, type ConnectionStatus } from "@/lib/api";
import { SlackChannelPicker } from "@/components/SlackChannelPicker";
import type { InputProps } from "./types";
import { interpolateHint } from "./types";

export type SlackChannelValue = {
  id: string;
  name: string;
  url: string;
};

export function SlackChannelInput({ schema, value, onChange }: InputProps<SlackChannelValue>) {
  const [conn, setConn] = useState<ConnectionStatus | null>(null);
  useEffect(() => {
    api
      .getConnections()
      .then(setConn)
      .catch(() => setConn({ slack: false, incidentio: false, slack_channel_prefix: "" }));
  }, []);

  const hint = schema.hint
    ? interpolateHint(schema.hint, value as unknown as Record<string, unknown>)
    : "";

  return (
    <label className="block">
      <span className="mb-1 block text-xs uppercase tracking-wide text-zinc-400">
        {schema.label}
      </span>
      {conn?.slack ? (
        <SlackChannelPicker
          value={value.id}
          onChange={(id, ch) =>
            onChange({ id, name: ch?.name ?? "", url: "" })
          }
        />
      ) : (
        <input
          type="url"
          value={value.url}
          onChange={(e) => onChange({ id: "", name: "", url: e.target.value })}
          placeholder={schema.placeholder ?? "https://example.slack.com/archives/C0123ABC"}
          className="w-full rounded border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-100 placeholder-zinc-600 focus:border-zinc-600 focus:outline-none"
        />
      )}
      {!conn?.slack && (
        <p className="mt-1 text-xs text-zinc-500">
          Slack isn&apos;t connected — paste a channel URL or wire a token in the Connections panel for the picker.
        </p>
      )}
      {hint && <p className="mt-1 whitespace-pre-line text-xs text-zinc-500">{hint}</p>}
    </label>
  );
}
