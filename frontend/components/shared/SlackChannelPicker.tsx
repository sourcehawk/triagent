"use client";

import { useEffect, useState } from "react";
import { api, ApiError, type SlackChannel } from "@/lib/api";
import { Spinner } from "@/components/shared/Spinner";
import { FilterableList } from "@/components/shared/FilterableList";
import { filterChannels } from "@/lib/slackChannels";

// SlackChannelPicker lists the channels the operator's slack token is a
// member of (joined-only — see spec
// 2026-05-08-slack-channel-picker-unification-design.md). The persistent
// note next to the filter input is part of the component itself so every
// caller surfaces the same wording.
type Props = {
  // Initial filter text; useful for the wiki modal which prefills "inc-".
  initialFilter?: string;
  // Fires when the operator picks a channel. Channel is undefined when
  // the operator clears the picker (currently unused — there's no clear
  // affordance — but the signature leaves room).
  onChange: (channelID: string, channel: SlackChannel | undefined) => void;
  // Currently-picked channel id, for the "selected" chip rendering.
  value: string;
};

export function SlackChannelPicker({ initialFilter, onChange, value }: Props) {
  const [channels, setChannels] = useState<SlackChannel[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .listSlackChannels()
      .then((cs) => {
        cs.sort((a, b) => a.name.localeCompare(b.name));
        setChannels(cs);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : String(e)));
  }, []);

  const note = (
    <>Only channels you are part of will be listed. Join a channel in Slack first if you don't see it here.</>
  );

  const picked = channels?.find((c) => c.id === value);
  if (picked) {
    return (
      <div className="space-y-2">
        <div className="flex items-baseline justify-between gap-3 rounded border border-zinc-800 bg-zinc-900/60 px-3 py-2">
          <span className="font-medium text-zinc-100">#{picked.name}</span>
          <button
            type="button"
            onClick={() => onChange("", undefined)}
            className="text-[11px] text-zinc-400 hover:text-zinc-200"
          >
            change
          </button>
        </div>
        <p className="text-[11px] text-zinc-500">{note}</p>
      </div>
    );
  }

  if (error) {
    return (
      <div className="rounded border border-red-900/60 bg-red-950/40 p-3 text-xs text-red-200/80">
        Failed to list channels: {error}
      </div>
    );
  }
  if (channels === null) {
    return (
      <div className="flex items-center gap-2 text-zinc-400">
        <Spinner /> fetching channels…
      </div>
    );
  }

  return (
    <FilterableList<SlackChannel>
      items={channels}
      filter={(c, q) => filterChannels([c], q).length > 0}
      itemKey={(c) => c.id}
      onPick={(c) => onChange(c.id, c)}
      placeholder="Filter channels by name…"
      initialFilter={initialFilter}
      belowFilter={note}
      listMaxHeightClass="max-h-[calc((100dvh-18rem)*0.4)]"
      emptyMessage={
        channels.length === 0
          ? "you're not in any public channels yet — join one in Slack and reload"
          : "no matches"
      }
      renderItem={(c) => (
        <span className="font-medium text-zinc-100">#{c.name}</span>
      )}
    />
  );
}
