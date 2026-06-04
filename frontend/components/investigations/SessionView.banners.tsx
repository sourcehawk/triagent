"use client";

import { useState } from "react";
import { takeover, restartAuto } from "@/lib/api";
import { Bot } from "lucide-react";

// AutoModeBanner is the prominent "auto mode is driving — click to
// take over" banner that sits above the transcript while phase is
// started or resumed. The whole banner is a button so the operator
// can click anywhere on it; the chip on the right is purely visual
// follow-through. Disabled while the POST is in flight so a double
// click can't fire takeover twice.
export function AutoModeBanner({ investigationId }: { investigationId: string }) {
  const [busy, setBusy] = useState(false);
  return (
    <button
      type="button"
      onClick={async () => {
        if (busy) return;
        setBusy(true);
        try {
          await takeover(investigationId);
        } catch {
          /* surfaced via SSE */
        } finally {
          setBusy(false);
        }
      }}
      className="w-full mb-3 flex items-center gap-3 rounded-lg border-2 border-pink-500/60 bg-pink-500/5 hover:bg-pink-500/10 transition-colors px-4 py-3 text-left cursor-pointer disabled:opacity-60 disabled:cursor-wait"
      disabled={busy}
      aria-label="Take over auto mode"
    >
      <Bot size={28} className="text-pink-500 flex-shrink-0" aria-hidden />
      <div className="flex-1 min-w-0">
        <div className="font-medium text-zinc-200">
          This session is running in auto mode
        </div>
        <div className="text-sm text-zinc-400">
          An operator agent is driving the chat. Click to take over.
        </div>
      </div>
      <span className="text-sm font-medium text-pink-400 whitespace-nowrap">
        {busy ? "Taking over…" : "Take over →"}
      </span>
    </button>
  );
}

// AutoModeFinishedBanner is the terminal-state companion to AutoModeBanner.
// Renders above the transcript when phase is "finished" or "aborted" so the
// operator can see at a glance that the auto-operator has closed out — plus
// the rationale and a Restart affordance. Finished phase = emerald; aborted
// phase = red. The composer below is enabled regardless, so the operator can
// still add manual notes after the session has wrapped.
export function AutoModeFinishedBanner({
  investigationId,
  phase,
  reason,
}: {
  investigationId: string;
  phase: "finished" | "aborted";
  reason: string | null;
}) {
  const [busy, setBusy] = useState(false);
  const aborted = phase === "aborted";
  return (
    <div
      role="region"
      aria-label={aborted ? "Auto mode aborted" : "Auto mode finished"}
      className={
        "mb-3 flex items-start gap-3 rounded-lg border-2 px-4 py-3 " +
        (aborted
          ? "border-red-500/60 bg-red-500/5"
          : "border-emerald-500/60 bg-emerald-500/5")
      }
    >
      <Bot
        size={28}
        className={
          "flex-shrink-0 " +
          (aborted ? "text-red-400" : "text-emerald-400")
        }
        aria-hidden
      />
      <div className="flex-1 min-w-0">
        <div className="font-medium text-zinc-200">
          {aborted ? "Auto mode aborted" : "Auto mode finished"}
        </div>
        <div className="text-sm text-zinc-400 break-words">
          {reason ?? (aborted
            ? "The operator agent stopped unexpectedly."
            : "The operator agent closed out the session.")}
        </div>
      </div>
      <button
        type="button"
        onClick={async () => {
          if (busy) return;
          setBusy(true);
          try {
            await restartAuto(investigationId);
          } catch {
            /* surfaced via SSE */
          } finally {
            setBusy(false);
          }
        }}
        disabled={busy}
        className={
          "text-sm font-medium whitespace-nowrap px-3 py-1 rounded-md transition-colors cursor-pointer disabled:opacity-60 disabled:cursor-wait " +
          (aborted
            ? "text-red-300 hover:bg-red-500/10"
            : "text-emerald-300 hover:bg-emerald-500/10")
        }
      >
        {busy ? "Restarting…" : "Restart auto mode"}
      </button>
    </div>
  );
}
