"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { CheckIcon, CopyIcon } from "./Icons";

type Tone = "zinc" | "amber" | "blue";
type Size = "sm" | "md";

type Props = {
  text: string;
  className?: string;
  tone?: Tone;
  // When `label` is omitted the button renders icon-only (a square,
  // SessionView-style affordance). When provided the icon sits next to
  // the label so the button reads like the older "copy URL" text
  // buttons in the modals.
  label?: string;
  copiedLabel?: string;
  size?: Size;
};

// Shared CopyButton: writes `text` to the clipboard and flashes a
// check for ~1.5s on success. All copy affordances in the frontend
// route through this so the icon + "copied" feedback stays uniform.
export function CopyButton({
  text,
  className,
  tone = "zinc",
  label,
  copiedLabel,
  size = "sm",
}: Props) {
  const [copied, setCopied] = useState(false);
  const timeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Clear any pending revert if the row unmounts while the check is
  // still showing — avoids a setState on a dead component.
  useEffect(() => {
    return () => {
      if (timeoutRef.current) clearTimeout(timeoutRef.current);
    };
  }, []);

  const onCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      if (timeoutRef.current) clearTimeout(timeoutRef.current);
      timeoutRef.current = setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard write can fail in insecure contexts; swallow
      // silently — the operator still has the on-screen text.
    }
  }, [text]);

  const toneIdle =
    tone === "amber"
      ? "border-amber-900/60 text-amber-300/80 hover:border-amber-700 hover:text-amber-200"
      : tone === "blue"
        ? "border-blue-900/60 text-blue-300/80 hover:border-blue-700 hover:text-blue-200"
        : "border-zinc-700 text-zinc-300 hover:border-zinc-500 hover:text-zinc-100";
  const toneCopied = "border-emerald-700/70 text-emerald-300";

  const isIconOnly = !label;
  const effectiveCopiedLabel = copiedLabel ?? "copied!";
  const iconSize = size === "md" ? "h-4 w-4" : "h-3.5 w-3.5";

  const sizing = isIconOnly
    ? size === "md"
      ? "h-7 w-7"
      : "h-6 w-6"
    : size === "md"
      ? "px-3 py-1.5 text-xs gap-1.5"
      : "px-2 py-0.5 text-xs gap-1";

  return (
    <button
      type="button"
      onClick={onCopy}
      aria-label={copied ? effectiveCopiedLabel : (label ?? "copy to clipboard")}
      title={copied ? "Copied!" : "Copy to clipboard"}
      className={
        "inline-flex items-center justify-center rounded border bg-transparent transition " +
        sizing +
        " " +
        (copied ? toneCopied : toneIdle) +
        (className ? " " + className : "")
      }
    >
      {copied ? (
        <CheckIcon className={iconSize} />
      ) : (
        <CopyIcon className={iconSize} />
      )}
      {label && (
        <span>{copied ? effectiveCopiedLabel : label}</span>
      )}
    </button>
  );
}
