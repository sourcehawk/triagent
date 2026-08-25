"use client";

import { Suspense } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useEffect, useState } from "react";
import { InvestigationForm, type FormSubmission } from "@/components/investigations/InvestigationForm";
import { api, ApiError } from "@/lib/api";

// Step-state for the *new investigation* flow at /investigations/new. Once
// preflight succeeds we router.push() to /investigations/<id> and
// SessionWorkspace takes over rendering on that route — this page never
// mounts SessionWorkspace.
type Step =
  | { kind: "form" }
  | { kind: "preflight" }
  | { kind: "error"; message: string };

// Next.js requires `useSearchParams` to be wrapped in a Suspense boundary
// for the static export. The default export is a thin Suspense shell;
// `InvestigationsHomeInner` is the actual page.
export default function InvestigationsHome() {
  return (
    <Suspense fallback={<HomeShell />}>
      <InvestigationsHomeInner />
    </Suspense>
  );
}

function HomeShell() {
  return (
    <main className="flex-1 overflow-y-auto">
      <Container>
        <Header />
      </Container>
    </main>
  );
}

function InvestigationsHomeInner() {
  const params = useSearchParams();
  const router = useRouter();
  const invParam = params?.get("inv") ?? null;

  const [step, setStep] = useState<Step>({ kind: "form" });

  // Legacy ?inv=<id> URLs redirect to the canonical /investigations/<id>
  // route. The active-investigation rendering lives there now.
  useEffect(() => {
    if (invParam) {
      router.replace(`/investigations/?id=${encodeURIComponent(invParam)}`);
    }
  }, [invParam, router]);

  async function runPreflight(sub: FormSubmission) {
    setStep({ kind: "preflight" });
    try {
      const inv = await api.preflight({
        inputs: sub.inputs,
        prom: sub.prom,
        auto: sub.auto || undefined,
        playbook: sub.playbook,
      });
      router.push(`/investigations/?id=${encodeURIComponent(inv.id)}`);
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : String(e);
      setStep({ kind: "error", message: msg });
    }
  }

  return (
    <main className="flex-1 overflow-y-auto">
      <Container>
        <Header />
        <NewStep
          step={step}
          onSubmit={runPreflight}
          onReset={() => setStep({ kind: "form" })}
        />
      </Container>
    </main>
  );
}

function Container({ children }: { children: React.ReactNode }) {
  return (
    <div className="mx-auto max-w-6xl px-6 py-10 space-y-8">{children}</div>
  );
}

function Header() {
  return (
    <header className="space-y-1">
      <h1 className="text-2xl font-semibold tracking-tight">Triagent</h1>
      <p className="text-sm text-zinc-400">
        Agentic Incident Investigation
      </p>
    </header>
  );
}

function NewStep({
  step,
  onSubmit,
  onReset,
}: {
  step: Step;
  onSubmit: (sub: FormSubmission) => void;
  onReset: () => void;
}) {
  switch (step.kind) {
    case "form":
      return (
        <Section title="Investigation details">
          <InvestigationForm
            onSubmit={onSubmit}
          />
        </Section>
      );
    case "preflight":
      return (
        <Section title="Running preflight…">
          <Status>
            <Spinner /> writing the per-session MCP config.
          </Status>
        </Section>
      );
    case "error":
      return (
        <Section title="Something went wrong">
          <div className="rounded border border-red-900/60 bg-red-950/40 p-3 text-sm text-red-200/90">
            {step.message}
          </div>
          <button
            type="button"
            onClick={onReset}
            className="mt-3 inline-flex items-center gap-1.5 text-sm text-zinc-400 hover:text-zinc-200"
          >
            <ArrowLeftIcon className="h-4 w-4" />
            start over
          </button>
        </Section>
      );
  }
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="space-y-3">
      <h2 className="text-sm font-medium text-zinc-300">{title}</h2>
      {children}
    </section>
  );
}

function Status({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex items-center gap-2 text-sm text-zinc-400">
      {children}
    </div>
  );
}

function Spinner() {
  return (
    <svg
      className="h-4 w-4 animate-spin"
      xmlns="http://www.w3.org/2000/svg"
      fill="none"
      viewBox="0 0 24 24"
    >
      <circle
        className="opacity-25"
        cx="12"
        cy="12"
        r="10"
        stroke="currentColor"
        strokeWidth="4"
      />
      <path
        className="opacity-75"
        fill="currentColor"
        d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"
      />
    </svg>
  );
}

function ArrowLeftIcon({ className }: { className?: string }) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 20 20"
      fill="currentColor"
      className={className}
    >
      <path
        fillRule="evenodd"
        d="M17 10a.75.75 0 01-.75.75H5.612l4.158 3.96a.75.75 0 11-1.04 1.08l-5.5-5.25a.75.75 0 010-1.08l5.5-5.25a.75.75 0 111.04 1.08L5.612 9.25H16.25A.75.75 0 0117 10z"
        clipRule="evenodd"
      />
    </svg>
  );
}
