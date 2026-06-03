export function Spinner({ className = "" }: { className?: string }) {
  return (
    <span
      aria-hidden
      className={`inline-block h-4 w-4 animate-spin rounded-full border-2 border-zinc-600 border-t-zinc-200 ${className}`}
    />
  );
}
