import { ArrowLeftIcon, ArrowRightIcon } from "@/components/Icons";

type PaginatorProps = {
  offset: number;
  pageSize: number;
  total: number;
  disabled?: boolean;
  onPageChange: (nextOffset: number) => void;
};

export function Paginator({
  offset,
  pageSize,
  total,
  disabled = false,
  onPageChange,
}: PaginatorProps) {
  const page = Math.floor(offset / pageSize) + 1;
  const pageCount = Math.max(1, Math.ceil(total / pageSize));
  const canPrev = offset > 0 && !disabled;
  const canNext = offset + pageSize < total && !disabled;

  const btn =
    "rounded border px-2.5 py-1 text-xs transition disabled:cursor-not-allowed disabled:opacity-50";
  const enabled =
    "border-zinc-700 text-zinc-300 hover:border-zinc-500 hover:text-zinc-100";
  const dim = "border-zinc-800 text-zinc-600";

  return (
    <div className="flex items-center justify-between gap-2 pt-1">
      <button
        type="button"
        className={`${btn} inline-flex items-center gap-1.5 ${canPrev ? enabled : dim}`}
        disabled={!canPrev}
        onClick={() => onPageChange(Math.max(0, offset - pageSize))}
      >
        <ArrowLeftIcon className="h-3.5 w-3.5" />
        prev
      </button>
      <span className="text-xs tabular-nums text-zinc-500">
        page {page} of {pageCount}
      </span>
      <button
        type="button"
        className={`${btn} inline-flex items-center gap-1.5 ${canNext ? enabled : dim}`}
        disabled={!canNext}
        onClick={() => onPageChange(offset + pageSize)}
      >
        next
        <ArrowRightIcon className="h-3.5 w-3.5" />
      </button>
    </div>
  );
}
