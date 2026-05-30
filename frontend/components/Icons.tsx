// Icon set for the launcher UI. Most icons are re-exports from
// lucide-react under the project's existing names, so the import
// surface — `import { CheckIcon, ExternalLinkIcon } from "@/components/Icons"`
// — stays unchanged at every callsite.
//
// The GitHub mark stays as a hand-trimmed Octicon: lucide dropped
// brand icons in v1, and the official Octicon is the right thing to
// render for a brand-mark anyway.

import {
  ArrowLeft,
  ArrowRight,
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ChevronUp,
  Cloud,
  CloudUpload,
  Copy,
  Download,
  ExternalLink,
  FileText,
  Pencil,
  RefreshCw,
  Trash2,
  TriangleAlert,
  Undo2,
} from "lucide-react";

type Props = { className?: string };

export const ArrowLeftIcon = ArrowLeft;
export const ArrowRightIcon = ArrowRight;
export const CheckIcon = Check;
export const CloudIcon = Cloud;
export const ChevronDownIcon = ChevronDown;
export const ChevronLeftIcon = ChevronLeft;
export const ChevronRightIcon = ChevronRight;
export const ChevronUpIcon = ChevronUp;
export const CopyIcon = Copy;
export const DocIcon = FileText;
export const DownloadIcon = Download;
export const EditIcon = Pencil;
export const ExternalLinkIcon = ExternalLink;
export const RevertIcon = Undo2;
export const SyncIcon = RefreshCw;
export const TrashIcon = Trash2;
export const UnsyncedIcon = CloudUpload;
export const WarningIcon = TriangleAlert;

// SlackIcon is the official 4-colour Slack mark. Inlined because lucide
// dropped brand icons; the path data is the canonical Slack-published
// SVG (CC-BY brand asset). Colours are baked in — the mark is only
// recognisable at full saturation, so callers don't override fill.
export function SlackIcon({ className }: Props) {
  return (
    <svg viewBox="0 0 24 24" className={className} aria-hidden>
      <path
        fill="#36C5F0"
        d="M8.834 5.042a2.528 2.528 0 0 1-2.521-2.52A2.528 2.528 0 0 1 8.834 0a2.528 2.528 0 0 1 2.521 2.522v2.52H8.834zM8.834 6.313a2.528 2.528 0 0 1 2.521 2.521 2.528 2.528 0 0 1-2.521 2.521H2.522A2.528 2.528 0 0 1 0 8.834a2.528 2.528 0 0 1 2.522-2.521h6.312z"
      />
      <path
        fill="#2EB67D"
        d="M18.956 8.834a2.528 2.528 0 0 1 2.522-2.521A2.528 2.528 0 0 1 24 8.834a2.528 2.528 0 0 1-2.522 2.521h-2.522V8.834zM17.688 8.834a2.528 2.528 0 0 1-2.523 2.521 2.527 2.527 0 0 1-2.52-2.521V2.522A2.527 2.527 0 0 1 15.165 0a2.528 2.528 0 0 1 2.523 2.522v6.312z"
      />
      <path
        fill="#ECB22E"
        d="M15.165 18.956a2.528 2.528 0 0 1 2.523 2.522A2.528 2.528 0 0 1 15.165 24a2.527 2.527 0 0 1-2.52-2.522v-2.522h2.52zM15.165 17.688a2.527 2.527 0 0 1-2.52-2.523 2.526 2.526 0 0 1 2.52-2.52h6.313A2.527 2.527 0 0 1 24 15.165a2.528 2.528 0 0 1-2.522 2.523h-6.313z"
      />
      <path
        fill="#E01E5A"
        d="M5.042 15.165a2.528 2.528 0 0 1-2.52 2.523A2.528 2.528 0 0 1 0 15.165a2.527 2.527 0 0 1 2.522-2.52h2.52v2.52zM6.313 15.165a2.527 2.527 0 0 1 2.521-2.52 2.527 2.527 0 0 1 2.521 2.52v6.313A2.528 2.528 0 0 1 8.834 24a2.528 2.528 0 0 1-2.521-2.522v-6.313z"
      />
    </svg>
  );
}

// IncidentIoIcon is a brand-suggestive mark — incident.io doesn't ship a
// publicly-canonical SVG mark the way Slack does, so this is a dark
// rounded square with an orange "i" stroke that reads as their identity
// at sidebar size. Swap for the official asset if/when it lands.
export function IncidentIoIcon({ className }: Props) {
  return (
    <svg viewBox="0 0 24 24" className={className} aria-hidden>
      <rect width="24" height="24" rx="5" fill="#0F0F0F" />
      <circle cx="12" cy="7.5" r="1.6" fill="#FF5C28" />
      <rect x="10.4" y="10.5" width="3.2" height="8.5" rx="1.2" fill="#FF5C28" />
    </svg>
  );
}

// ChatBubbleIcon is the editor chat-toggle glyph used by both the
// playbook and wiki editors. Stroke-only line so it renders cleanly
// at small sizes alongside the action-row labels.
export function ChatBubbleIcon({ className }: Props) {
  return (
    <svg
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden
    >
      <path d="M3 6.5a3 3 0 0 1 3-3h4a3 3 0 0 1 3 3v3a3 3 0 0 1-3 3H7l-3 2.5v-2.5h-1a3 3 0 0 1-1-2.16Z" />
    </svg>
  );
}

// GitHubIcon is the official Octicon (16px GitHub mark, MIT). lucide
// stopped shipping brand icons, so this stays inline — it's the
// canonical mark to render in any "open on GitHub" affordance.
export function GitHubIcon({ className }: Props) {
  return (
    <svg
      viewBox="0 0 16 16"
      fill="currentColor"
      className={className}
      aria-hidden
    >
      <path
        fillRule="evenodd"
        d="M8 0C3.58 0 0 3.58 0 8a8 8 0 0 0 5.47 7.59c.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0 0 16 8c0-4.42-3.58-8-8-8z"
      />
    </svg>
  );
}
