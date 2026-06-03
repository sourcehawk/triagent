"use client";

import { useCallback, useEffect, useState } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import { SessionWorkspace } from "@/components/investigations/SessionWorkspace";
import { Spinner } from "@/components/shared/Spinner";
import { ArrowLeftIcon } from "@/components/shared/Icons";
import { api, ApiError, type Investigation } from "@/lib/api";

type LoadState =
  | { kind: "loading" }
  | { kind: "active"; investigation: Investigation }
  | { kind: "missing" }
  | { kind: "error"; message: string };

export function InvestigationPageClient() {
  const searchParams = useSearchParams();
  const router = useRouter();
  // Reading via useSearchParams() re-renders on intra-route navigation
  // (clicking between session entries in the sidebar). Soft-nav within
  // the same /investigations/ route preserves the layout + the
  // StreamProvider, so we don't lose the SSE connection across clicks.
  const id = searchParams?.get("id") ?? "";
  const [state, setState] = useState<LoadState>({ kind: "loading" });

  useEffect(() => {
    if (!id) return;
    setState({ kind: "loading" });
    let cancelled = false;
    api
      .getInvestigation(id)
      .then((inv) => {
        if (cancelled) return;
        setState({ kind: "active", investigation: inv });
        // Notify the sidebar (mounted in a sibling subtree) about this
        // investigation. Covers two cases:
        //   - Newly-created investigation that wasn't in the sidebar's
        //     initial /api/investigations fetch
        //   - Stale label or status on the existing sidebar item from a
        //     prior fetch
        // Sidebar's listener upserts on this event.
        if (typeof window !== "undefined") {
          window.dispatchEvent(
            new CustomEvent("triagent:investigation-changed", {
              detail: inv,
            }),
          );
        }
      })
      .catch((e) => {
        if (cancelled) return;
        if (e instanceof ApiError && e.status === 404) {
          setState({ kind: "missing" });
        } else {
          setState({
            kind: "error",
            message: e instanceof ApiError ? e.message : String(e),
          });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [id]);

  const onReset = useCallback(() => {
    router.push("/");
  }, [router]);

  if (!id || state.kind === "loading") {
    return (
      <main className="flex-1 overflow-y-auto">
        <div className="mx-auto max-w-6xl px-6 py-10">
          <div className="flex items-center gap-2 text-sm text-zinc-400">
            <Spinner /> loading investigation…
          </div>
        </div>
      </main>
    );
  }
  if (state.kind === "missing") {
    return (
      <main className="flex-1 overflow-y-auto">
        <div className="mx-auto max-w-6xl px-6 py-10 space-y-3">
          <h2 className="text-sm font-medium text-zinc-300">
            Investigation not found
          </h2>
          <p className="text-sm text-zinc-400">
            The id{" "}
            <code className="rounded bg-zinc-800 px-1.5 py-0.5 font-mono text-xs">
              {id}
            </code>{" "}
            isn't on this launcher. It may have been deleted, or this launcher
            started against a different sessions root.
          </p>
          <button
            type="button"
            onClick={onReset}
            className="mt-3 inline-flex items-center gap-1.5 text-sm text-zinc-400 hover:text-zinc-200"
          >
            <ArrowLeftIcon className="h-4 w-4" />
            start a new investigation
          </button>
        </div>
      </main>
    );
  }
  if (state.kind === "error") {
    return (
      <main className="flex-1 overflow-y-auto">
        <div className="mx-auto max-w-6xl px-6 py-10 space-y-3">
          <h2 className="text-sm font-medium text-zinc-300">
            Something went wrong
          </h2>
          <div className="rounded border border-red-900/60 bg-red-950/40 p-3 text-sm text-red-200/90">
            {state.message}
          </div>
          <button
            type="button"
            onClick={onReset}
            className="mt-3 inline-flex items-center gap-1.5 text-sm text-zinc-400 hover:text-zinc-200"
          >
            <ArrowLeftIcon className="h-4 w-4" />
            start over
          </button>
        </div>
      </main>
    );
  }
  return (
    <main className="flex-1 overflow-y-auto">
      <div className="h-full">
        <SessionWorkspace
          key={state.investigation.id}
          investigation={state.investigation}
          onReset={onReset}
        />
      </div>
    </main>
  );
}
