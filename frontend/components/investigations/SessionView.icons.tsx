"use client";

import { Spinner } from "@/components/shared/Spinner";

export function PlayIcon({ className }: { className?: string }) {
  return (
    // Filled right-pointing triangle. Slight horizontal offset (start
    // at x=4, not x=3) optically centers the wedge inside the round
    // chip — geometric centering would feel left-heavy because the
    // tip is the visual anchor.
    <svg
      viewBox="0 0 16 16"
      fill="currentColor"
      className={className}
      aria-hidden
    >
      <path d="M5 3.5l7 4.5-7 4.5z" />
    </svg>
  );
}

// HandIcon is the "stop / take over" affordance on the auto-mode
// composer chip. A square outline reads as a "stop" glyph at this
// size; geometric centering is fine here (no asymmetric tip like the
// play wedge has).
export function HandIcon({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 16 16"
      fill="currentColor"
      className={className}
      aria-hidden
    >
      <rect x="4" y="4" width="8" height="8" rx="1" />
    </svg>
  );
}

// StopIcon is the chip used when streaming — replaces Send so the
// operator can interrupt the in-flight turn (ChatGPT-style). Same
// geometry as HandIcon but a separate component so its semantic
// purpose stays distinct in the source.
export function StopIcon({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 16 16"
      fill="currentColor"
      className={className}
      aria-hidden
    >
      <rect x="4" y="4" width="8" height="8" rx="1" />
    </svg>
  );
}

export function Working() {
  return (
    <div className="mt-3 flex items-center gap-2 px-1 text-xs text-zinc-500">
      <Spinner className="h-3 w-3" />
      <span>working…</span>
    </div>
  );
}
