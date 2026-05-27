"use client";

import { useEffect, useState } from "react";
import { ApiError, type Capabilities } from "@/lib/api";
import {
  wikiApi,
  type WikiBundleFile,
  type WikiOpenPR,
  type WikiPushBundleResult,
} from "@/lib/wiki-api";
import { Spinner } from "@/components/Spinner";
import { CopyButton } from "@/components/CopyButton";

// PushWikiPRModal opens an upstream PR that bundles every locally-
// committed-but-not-pushed file. The editor builds the bundle (root +
// linked dirty files) and passes it in. Title/branch defaults are
// derived from the rootDisplay; the operator can override before
// submitting.

type Bundle = {
  rootDisplay: string;
  files: WikiBundleFile[];
};

type Props = {
  bundle: Bundle;
  capabilities: Capabilities;
  onClose: () => void;
  onSuccess?: () => void;
};

export function PushWikiPRModal({
  bundle,
  capabilities,
  onClose,
  onSuccess,
}: Props) {
  const today = new Date().toISOString().slice(0, 10).replace(/-/g, "");
  const safeRoot = bundle.rootDisplay.replace(/\//g, "-");

  const [branch, setBranch] = useState(`wiki/edit-${safeRoot}-${today}`);
  const [title, setTitle] = useState(
    bundle.files.length > 1
      ? `wiki: edit ${bundle.rootDisplay} + ${bundle.files.length - 1} linked`
      : `wiki: edit ${bundle.rootDisplay}`,
  );
  // When the upstream wiki repo is shared with other artifact types
  // (the default), capabilities.wiki.subpath carries the in-repo dir
  // where entries/ and entities/ live (e.g. "wikis"). Prefix it onto
  // the path bullets so the PR body matches the actual commit path.
  const wikiSubpath = capabilities.wiki.subpath ?? "";
  const prefix = wikiSubpath ? `${wikiSubpath}/` : "";
  const [body, setBody] = useState(() => {
    const lines = [
      "Edits to the following wiki files from the Triagent launcher:",
      "",
    ];
    for (const f of bundle.files) {
      if (f.kind === "entry") {
        lines.push(`- \`${prefix}entries/${f.slug}.md\``);
      } else {
        lines.push(
          `- \`${prefix}entities/${f.entity_type}/${f.entity_name}.md\``,
        );
      }
    }
    return lines.join("\n");
  });
  const [base, setBase] = useState("main");
  const [pushing, setPushing] = useState(false);
  const [errors, setErrors] = useState<string[] | null>(null);
  const [result, setResult] = useState<WikiPushBundleResult | null>(null);
  const [openPRs, setOpenPRs] = useState<WikiOpenPR[]>([]);

  // Close on Escape.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape" && !pushing) onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [pushing, onClose]);

  // Look up existing open PRs for this root so the operator can spot
  // in-flight work before opening a duplicate. Best-effort: when gh is
  // unavailable the endpoint returns an empty list, and we surface
  // nothing rather than blocking the push.
  useEffect(() => {
    let cancelled = false;
    wikiApi
      .listOpenPRs(bundle.rootDisplay)
      .then((res) => {
        if (cancelled) return;
        setOpenPRs(res.prs);
      })
      .catch(() => {
        if (cancelled) return;
        setOpenPRs([]);
      });
    return () => {
      cancelled = true;
    };
  }, [bundle.rootDisplay]);

  async function push() {
    setPushing(true);
    setErrors(null);
    try {
      const res = await wikiApi.pushPRBundle({
        files: bundle.files,
        root_display: bundle.rootDisplay,
        branch,
        base,
        title,
        body,
      });
      if (res.ok) {
        setResult(res);
        onSuccess?.();
      } else {
        const errs = res.errors ?? ["push failed"];
        setErrors(errs);
      }
    } catch (e) {
      setErrors([e instanceof ApiError ? e.message : String(e)]);
    } finally {
      setPushing(false);
    }
  }

  return (
    <div
      className="fixed inset-0 z-[60] flex items-center justify-center bg-black/60 p-4"
      onClick={(e) => {
        if (e.target === e.currentTarget && !pushing) onClose();
      }}
    >
      <div className="w-full max-w-xl rounded border border-zinc-700 bg-zinc-950 p-5 shadow-2xl">
        {result ? (
          <SuccessView
            result={result}
            openPRs={openPRs.filter((p) => p.url !== result.url)}
            onClose={onClose}
          />
        ) : (
          <>
            <header className="mb-4 space-y-1">
              <h2 className="text-base font-semibold text-zinc-100">
                Push wiki edits as PR
              </h2>
              <p className="text-xs text-zinc-500">
                {capabilities.gh.user
                  ? `Will push as ${capabilities.gh.user} from `
                  : "Will push from "}
                <code className="rounded bg-zinc-800 px-1 py-0.5 font-mono text-xs text-zinc-300 break-all">
                  {capabilities.wiki.path ?? "wiki vault"}
                </code>{" "}
                → PR opens against{" "}
                <code className="rounded bg-zinc-800 px-1 py-0.5 font-mono text-xs text-zinc-300">
                  {capabilities.wiki.repo ?? "wiki repo"}
                </code>
                .
              </p>
              <p className="text-xs text-zinc-500">
                Bundling{" "}
                <span className="font-medium text-zinc-300">
                  {bundle.files.length} file{bundle.files.length !== 1 ? "s" : ""}
                </span>
                : {bundle.files
                  .slice(0, 4)
                  .map((f) =>
                    f.kind === "entry"
                      ? f.slug
                      : `${f.entity_type}/${f.entity_name}`,
                  )
                  .join(", ")}
                {bundle.files.length > 4 && ` + ${bundle.files.length - 4} more`}
              </p>
            </header>

            {openPRs.length > 0 && <OpenPRsBanner prs={openPRs} stage="pre" />}

            <div className="space-y-3">
              <Field label="branch">
                <input
                  value={branch}
                  onChange={(e) => setBranch(e.target.value)}
                  disabled={pushing}
                  className={inputClass}
                />
              </Field>
              <Field label="base">
                <input
                  value={base}
                  onChange={(e) => setBase(e.target.value)}
                  disabled={pushing}
                  className={inputClass}
                />
              </Field>
              <Field label="title">
                <input
                  value={title}
                  onChange={(e) => setTitle(e.target.value)}
                  disabled={pushing}
                  className={inputClass}
                />
              </Field>
              <Field label="body">
                <textarea
                  value={body}
                  onChange={(e) => setBody(e.target.value)}
                  disabled={pushing}
                  rows={6}
                  className={`${inputClass} resize-y`}
                />
              </Field>

              {errors && errors.length > 0 && (
                <div className="rounded border border-red-900/60 bg-red-950/40 p-2 text-xs text-red-200/90">
                  <div className="mb-1 font-medium">Push failed:</div>
                  <ul className="space-y-0.5">
                    {errors.map((e, i) => (
                      <li key={i}>• {e}</li>
                    ))}
                  </ul>
                </div>
              )}
            </div>

            <footer className="mt-5 flex items-center justify-end gap-2">
              <button
                type="button"
                onClick={onClose}
                disabled={pushing}
                className="rounded border border-zinc-800 px-3 py-1.5 text-xs text-zinc-400 transition hover:text-zinc-200 disabled:opacity-50"
              >
                cancel
              </button>
              <button
                type="button"
                onClick={push}
                disabled={
                  pushing ||
                  !branch.trim() ||
                  !title.trim() ||
                  !base.trim() ||
                  bundle.files.length === 0
                }
                className="flex items-center gap-2 rounded bg-zinc-100 px-3 py-1.5 text-xs font-medium text-zinc-900 transition hover:bg-white disabled:cursor-not-allowed disabled:bg-zinc-700 disabled:text-zinc-400"
              >
                {pushing && (
                  <Spinner className="h-3 w-3 border-zinc-500 border-t-zinc-900" />
                )}
                {pushing ? "pushing…" : "push & open PR"}
              </button>
            </footer>
          </>
        )}
      </div>
    </div>
  );
}

function SuccessView({
  result,
  openPRs,
  onClose,
}: {
  result: WikiPushBundleResult;
  openPRs: WikiOpenPR[];
  onClose: () => void;
}) {
  // Open the freshly-created PR in a new tab. Browsers may block
  // window.open() outside a user gesture, but the push button click
  // that triggered this success view often counts; if not, the link
  // immediately below acts as the visible fallback.
  useEffect(() => {
    if (!result.url) return;
    window.open(result.url, "_blank", "noopener,noreferrer");
  }, [result.url]);

  return (
    <>
      <header className="mb-3 space-y-1">
        <h2 className="text-base font-semibold text-emerald-300">
          Pull request opened
        </h2>
        <p className="text-xs text-zinc-500">
          Branch{" "}
          <code className="rounded bg-zinc-800 px-1 py-0.5 font-mono text-xs text-zinc-300">
            {result.branch}
          </code>{" "}
          →{" "}
          <code className="rounded bg-zinc-800 px-1 py-0.5 font-mono text-xs text-zinc-300">
            {result.base}
          </code>
        </p>
      </header>
      <div className="rounded border border-zinc-800 bg-zinc-900/40 p-3">
        <a
          href={result.url}
          target="_blank"
          rel="noopener noreferrer"
          className="break-all font-mono text-xs text-sky-300 underline hover:text-sky-200"
        >
          {result.url}
        </a>
      </div>
      {openPRs.length > 0 && (
        <div className="mt-3">
          <OpenPRsBanner prs={openPRs} stage="post" />
        </div>
      )}
      <footer className="mt-5 flex items-center justify-end gap-2">
        {result.url && <CopyButton text={result.url} label="copy URL" size="md" />}
        {result.url && (
          <a
            href={result.url}
            target="_blank"
            rel="noopener noreferrer"
            className="rounded bg-zinc-100 px-3 py-1.5 text-xs font-medium text-zinc-900 transition hover:bg-white"
          >
            open in browser
          </a>
        )}
        <button
          type="button"
          onClick={onClose}
          className="rounded border border-zinc-800 px-3 py-1.5 text-xs text-zinc-400 transition hover:text-zinc-200"
        >
          close
        </button>
      </footer>
    </>
  );
}

// OpenPRsBanner renders the "existing open PRs for this wiki page"
// warning. `stage` switches between two phrasings — "before you push"
// vs "alongside the one you just opened" — without splitting the
// component in two.
function OpenPRsBanner({
  prs,
  stage,
}: {
  prs: WikiOpenPR[];
  stage: "pre" | "post";
}) {
  const heading =
    stage === "pre"
      ? `Open PR${prs.length === 1 ? "" : "s"} already touch${prs.length === 1 ? "es" : ""} this page`
      : `Other open PR${prs.length === 1 ? "" : "s"} for this page`;
  const subtitle =
    stage === "pre"
      ? "Consider iterating on the existing branch instead of opening a duplicate."
      : "Heads-up — you may want to close or rebase these.";
  return (
    <div className="mb-3 rounded border border-amber-900/60 bg-amber-950/30 p-3 text-xs text-amber-100/90">
      <div className="mb-1 font-medium text-amber-200">{heading}</div>
      <div className="mb-2 text-amber-100/70">{subtitle}</div>
      <ul className="space-y-1">
        {prs.map((pr) => (
          <li key={pr.number} className="break-all">
            <a
              href={pr.url}
              target="_blank"
              rel="noopener noreferrer"
              className="text-sky-300 underline hover:text-sky-200"
            >
              #{pr.number}
            </a>{" "}
            <span className="text-amber-100/90">{pr.title}</span>{" "}
            <code className="rounded bg-zinc-900/60 px-1 py-0.5 font-mono text-[10px] text-zinc-400">
              {pr.branch}
            </code>
          </li>
        ))}
      </ul>
    </div>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <label className="block">
      <span className="mb-1 block text-xs uppercase tracking-wide text-zinc-400">
        {label}
      </span>
      {children}
    </label>
  );
}

const inputClass =
  "w-full rounded border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-sm text-zinc-100 placeholder-zinc-600 focus:border-zinc-600 focus:outline-none disabled:opacity-60";
