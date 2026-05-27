"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { createPortal } from "react-dom";

// Async dialogs replace the browser-native confirm / alert / prompt.
// One DialogProvider sits at the layout root; components call
// useDialog() to get back imperative `confirm`, `alert`, `prompt`
// helpers that resolve when the user closes the modal. This keeps
// callsites tiny — they don't need to thread modal state through every
// component — while giving us full visual control.
//
// The provider also hosts a non-blocking toast surface (useDialog().
// notify / update / dismiss) so long-running background jobs (PR pushes,
// upstream syncs, …) can report progress + completion from anywhere on
// the page without holding the user inside a modal.

export type ConfirmOptions = {
  title: string;
  // Long-form context shown below the title. Single string or split
  // across multiple paragraphs via embedded newlines.
  body?: string;
  // Default labels: "Confirm" / "Cancel".
  confirmLabel?: string;
  cancelLabel?: string;
  // Renders the confirm button red, for destructive actions like
  // delete-node, delete-playbook, or delete-investigation.
  danger?: boolean;
};

export type AlertOptions = {
  title: string;
  body?: string;
  // Defaults to "OK".
  dismissLabel?: string;
};

export type PromptOptions = {
  title: string;
  body?: string;
  placeholder?: string;
  defaultValue?: string;
  confirmLabel?: string;
  cancelLabel?: string;
  // Optional sync validator. Returning a string surfaces it as an
  // inline error and keeps the modal open. Also runs continuously as
  // the user types; while it returns non-null the confirm button is
  // disabled.
  validate?: (value: string) => string | null;
  // Style the confirm button as a destructive action (outlined red).
  // Use for delete / destroy flows so the affirmative isn't visually
  // identical to a "Save changes" CTA.
  danger?: boolean;
};

export type ToastKind = "pending" | "success" | "error" | "info";

export type ToastOptions = {
  kind: ToastKind;
  title: string;
  // Optional second line, e.g. an error detail or a hint.
  body?: string;
  // Optional "open" action — e.g. a link to the PR that just landed.
  // Renders as a small button to the right of the title.
  action?: { label: string; href: string };
  // persistent: true overrides everything else and pins the toast until
  // the operator clicks ✕. Use for results the operator MUST see (e.g.
  // a freshly-opened PR URL — losing it after 8s costs them the link).
  persistent?: boolean;
  // ttlMs auto-dismisses after the given number of milliseconds. Omit
  // (or set 0) to make the toast sticky until the operator dismisses it
  // by clicking the ✕. "pending" toasts ignore ttlMs and stay sticky
  // until update() flips them to success/error/info. Ignored when
  // persistent is true.
  ttlMs?: number;
};

export type DialogAPI = {
  confirm(opts: ConfirmOptions): Promise<boolean>;
  alert(opts: AlertOptions): Promise<void>;
  prompt(opts: PromptOptions): Promise<string | null>;
  // notify pushes a toast onto the stack and returns its id so the
  // caller can later update() or dismiss() it.
  notify(opts: ToastOptions): string;
  // update mutates an existing toast in place — typically to flip a
  // "pending" toast into "success" or "error" once the underlying
  // background job settles.
  update(id: string, opts: ToastOptions): void;
  // dismiss removes the toast immediately.
  dismiss(id: string): void;
};

type ActiveDialog =
  | { kind: "confirm"; opts: ConfirmOptions; resolve: (v: boolean) => void }
  | { kind: "alert"; opts: AlertOptions; resolve: () => void }
  | {
      kind: "prompt";
      opts: PromptOptions;
      resolve: (v: string | null) => void;
    };

type ActiveToast = ToastOptions & { id: string };

const Ctx = createContext<DialogAPI | null>(null);

let toastSeq = 0;
function nextToastID(): string {
  toastSeq += 1;
  return `t-${Date.now()}-${toastSeq}`;
}

export function DialogProvider({ children }: { children: React.ReactNode }) {
  const [active, setActive] = useState<ActiveDialog | null>(null);
  // Keep a ref to the active dialog so the Esc handler always sees the
  // current one (state in a closure would lag).
  const activeRef = useRef<ActiveDialog | null>(null);
  activeRef.current = active;

  // Portal target. Resolved after mount so SSR doesn't try to touch
  // document.body. Portaling to <body> escapes any stacking context a
  // parent (e.g. a `fixed z-50` modal that spawned the confirm) might
  // create — without it, the dialog can render *under* the modal that
  // opened it, hidden from the operator.
  const [portalRoot, setPortalRoot] = useState<HTMLElement | null>(null);
  useEffect(() => {
    setPortalRoot(document.body);
  }, []);

  const [toasts, setToasts] = useState<ActiveToast[]>([]);
  // Track active auto-dismiss timers so we can cancel them when a
  // toast is updated or dismissed before its TTL fires.
  const timersRef = useRef<Map<string, ReturnType<typeof setTimeout>>>(
    new Map(),
  );

  const close = useCallback(<T,>(value: T) => {
    const a = activeRef.current;
    if (!a) return;
    setActive(null);
    // Defer the resolve so React finishes its render before the
    // promise's then runs (avoids an awkward "open new dialog from
    // within a dialog close" double-render glitch).
    queueMicrotask(() => {
      if (a.kind === "alert") (a.resolve as (v: void) => void)();
      else (a.resolve as (v: T) => void)(value);
    });
  }, []);

  const dismiss = useCallback((id: string) => {
    const t = timersRef.current.get(id);
    if (t) {
      clearTimeout(t);
      timersRef.current.delete(id);
    }
    setToasts((prev) => prev.filter((x) => x.id !== id));
  }, []);

  const scheduleAutoDismiss = useCallback(
    (id: string, opts: ToastOptions) => {
      // Always start by clearing any previous timer for this id —
      // notably when update() flips a pending → success that wants to
      // stay sticky, we must not let an old timer fire and remove it.
      const existing = timersRef.current.get(id);
      if (existing) {
        clearTimeout(existing);
        timersRef.current.delete(id);
      }
      // persistent + pending always stay until the caller dismisses or
      // flips them.
      if (opts.persistent) return;
      if (opts.kind === "pending") return;
      const ttl =
        opts.ttlMs !== undefined
          ? opts.ttlMs
          : opts.kind === "error"
            ? 0
            : 8000;
      if (!ttl || ttl <= 0) return;
      const t = setTimeout(() => {
        timersRef.current.delete(id);
        setToasts((prev) => prev.filter((x) => x.id !== id));
      }, ttl);
      timersRef.current.set(id, t);
    },
    [],
  );

  const notify = useCallback(
    (opts: ToastOptions): string => {
      const id = nextToastID();
      setToasts((prev) => [...prev, { ...opts, id }]);
      scheduleAutoDismiss(id, opts);
      return id;
    },
    [scheduleAutoDismiss],
  );

  const update = useCallback(
    (id: string, opts: ToastOptions) => {
      setToasts((prev) =>
        prev.map((t) => (t.id === id ? { ...opts, id } : t)),
      );
      scheduleAutoDismiss(id, opts);
    },
    [scheduleAutoDismiss],
  );

  // Clear pending timers on unmount.
  useEffect(() => {
    const map = timersRef.current;
    return () => {
      map.forEach(clearTimeout);
      map.clear();
    };
  }, []);

  // Each callback below is stable (empty / known deps) and the api
  // object itself is memoized over those callbacks. Without the
  // useMemo wrapper, the api literal would get a fresh identity on
  // every DialogProvider render — including renders triggered by
  // toast state — which would invalidate any consumer's
  // useEffect(..., [dialog]) dependency and force EventSources or
  // similar long-lived setups to tear down and rebuild on each toast.
  const confirm = useCallback(
    (opts: ConfirmOptions) =>
      new Promise<boolean>((resolve) => {
        setActive({ kind: "confirm", opts, resolve });
      }),
    [],
  );
  const alert = useCallback(
    (opts: AlertOptions) =>
      new Promise<void>((resolve) => {
        setActive({ kind: "alert", opts, resolve });
      }),
    [],
  );
  const prompt = useCallback(
    (opts: PromptOptions) =>
      new Promise<string | null>((resolve) => {
        setActive({ kind: "prompt", opts, resolve });
      }),
    [],
  );

  const api = useMemo<DialogAPI>(
    () => ({ confirm, alert, prompt, notify, update, dismiss }),
    [confirm, alert, prompt, notify, update, dismiss],
  );

  return (
    <Ctx.Provider value={api}>
      {children}
      {portalRoot &&
        active &&
        createPortal(
          <DialogView active={active} onClose={close} />,
          portalRoot,
        )}
      {portalRoot &&
        createPortal(
          <ToastStack toasts={toasts} onDismiss={dismiss} />,
          portalRoot,
        )}
    </Ctx.Provider>
  );
}

export function useDialog(): DialogAPI {
  const ctx = useContext(Ctx);
  if (!ctx)
    throw new Error("useDialog must be used inside <DialogProvider>");
  return ctx;
}

function DialogView({
  active,
  onClose,
}: {
  active: ActiveDialog;
  onClose: <T>(value: T) => void;
}) {
  const [promptValue, setPromptValue] = useState(
    active.kind === "prompt" ? active.opts.defaultValue ?? "" : "",
  );
  const [promptError, setPromptError] = useState<string | null>(null);

  // Esc closes (cancel for confirm/prompt, dismiss for alert).
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        if (active.kind === "alert") onClose(undefined);
        else if (active.kind === "confirm") onClose(false);
        else onClose(null);
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [active, onClose]);

  // For prompt: Enter submits.
  function onPromptKey(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === "Enter") {
      e.preventDefault();
      submitPrompt();
    }
  }

  function submitPrompt() {
    if (active.kind !== "prompt") return;
    if (active.opts.validate) {
      const err = active.opts.validate(promptValue);
      if (err) {
        setPromptError(err);
        return;
      }
    }
    onClose(promptValue);
  }

  const danger =
    (active.kind === "confirm" && active.opts.danger) ||
    (active.kind === "prompt" && active.opts.danger);

  // Live validation: keep the affirmative button disabled while the
  // current input would be rejected. Same predicate the submit path
  // uses; running it on every keystroke avoids the post-click jarring
  // "Type X to confirm" reveal.
  const promptInvalid =
    active.kind === "prompt" && !!active.opts.validate && active.opts.validate(promptValue) !== null;
  const title =
    active.kind === "alert" || active.kind === "confirm" || active.kind === "prompt"
      ? active.opts.title
      : "";
  const body = active.opts.body;

  return (
    <div
      className="fixed inset-0 z-[100] flex items-center justify-center bg-black/60 p-4"
      onClick={(e) => {
        if (e.target === e.currentTarget) {
          if (active.kind === "alert") onClose(undefined);
          else if (active.kind === "confirm") onClose(false);
          else onClose(null);
        }
      }}
    >
      <div className="w-full max-w-md rounded border border-zinc-700 bg-zinc-950 p-5 shadow-2xl">
        <h2
          className={
            "text-base font-semibold " +
            (danger ? "text-red-300" : "text-zinc-100")
          }
        >
          {title}
        </h2>
        {body && (
          <p className="mt-2 whitespace-pre-wrap text-sm text-zinc-400">
            {body}
          </p>
        )}

        {active.kind === "prompt" && (
          <div className="mt-4 space-y-1">
            <input
              autoFocus
              value={promptValue}
              onChange={(e) => {
                setPromptValue(e.target.value);
                if (promptError) setPromptError(null);
              }}
              onKeyDown={onPromptKey}
              placeholder={active.opts.placeholder}
              className="w-full rounded border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-sm text-zinc-100 placeholder-zinc-600 focus:border-zinc-600 focus:outline-none"
            />
            {promptError && (
              <p className="text-xs text-red-400">{promptError}</p>
            )}
          </div>
        )}

        <footer className="mt-5 flex items-center justify-end gap-2">
          {active.kind !== "alert" && (
            <button
              type="button"
              onClick={() =>
                onClose(active.kind === "confirm" ? false : null)
              }
              className="rounded border border-zinc-800 px-3 py-1.5 text-xs text-zinc-400 transition hover:text-zinc-200"
            >
              {active.opts.cancelLabel ?? "Cancel"}
            </button>
          )}
          {active.kind === "alert" && (
            <button
              type="button"
              autoFocus
              onClick={() => onClose(undefined)}
              className="rounded bg-zinc-100 px-3 py-1.5 text-xs font-medium text-zinc-900 transition hover:bg-white"
            >
              {active.opts.dismissLabel ?? "OK"}
            </button>
          )}
          {active.kind === "confirm" && (
            <button
              type="button"
              autoFocus
              onClick={() => onClose(true)}
              className={
                "rounded px-3 py-1.5 text-xs font-medium transition " +
                (danger
                  ? "border border-red-900/60 text-red-300 hover:border-red-800 hover:bg-red-950/30 hover:text-red-200"
                  : "bg-zinc-100 text-zinc-900 hover:bg-white")
              }
            >
              {active.opts.confirmLabel ?? "Confirm"}
            </button>
          )}
          {active.kind === "prompt" && (
            <button
              type="button"
              onClick={submitPrompt}
              disabled={promptInvalid}
              className={
                "rounded px-3 py-1.5 text-xs font-medium transition disabled:cursor-not-allowed disabled:opacity-50 " +
                (danger
                  ? "border border-red-900/60 text-red-300 hover:border-red-800 hover:bg-red-950/30 hover:text-red-200 disabled:hover:border-red-900/60 disabled:hover:bg-transparent disabled:hover:text-red-300"
                  : "bg-zinc-100 text-zinc-900 hover:bg-white disabled:hover:bg-zinc-100")
              }
            >
              {active.opts.confirmLabel ?? "OK"}
            </button>
          )}
        </footer>
      </div>
    </div>
  );
}

// ToastStack renders the queue of active toasts at the bottom-right of the
// viewport. Stacks vertically, newest at the bottom. Each toast renders a
// kind-tinted left border, the title, optional body, optional action link,
// and a close button.
function ToastStack({
  toasts,
  onDismiss,
}: {
  toasts: ActiveToast[];
  onDismiss: (id: string) => void;
}) {
  if (toasts.length === 0) return null;
  return (
    <div className="pointer-events-none fixed bottom-4 right-4 z-[110] flex w-[22rem] flex-col gap-2">
      {toasts.map((t) => (
        <ToastView key={t.id} toast={t} onDismiss={() => onDismiss(t.id)} />
      ))}
    </div>
  );
}

function ToastView({
  toast,
  onDismiss,
}: {
  toast: ActiveToast;
  onDismiss: () => void;
}) {
  const tone = toneFor(toast.kind);
  return (
    <div
      role={toast.kind === "error" ? "alert" : "status"}
      className={
        "pointer-events-auto flex items-start gap-3 rounded border bg-zinc-950/95 p-3 shadow-2xl backdrop-blur " +
        tone.border
      }
    >
      <div className={"mt-0.5 shrink-0 " + tone.icon}>
        {toast.kind === "pending" ? (
          <span
            className="block h-3 w-3 animate-spin rounded-full border-2 border-current border-r-transparent"
            aria-hidden
          />
        ) : (
          <ToastGlyph kind={toast.kind} />
        )}
      </div>
      <div className="min-w-0 flex-1 space-y-0.5">
        <div className="text-sm font-medium text-zinc-100">{toast.title}</div>
        {toast.body && (
          <div className="text-xs text-zinc-400">{toast.body}</div>
        )}
        {toast.action && (
          <a
            href={toast.action.href}
            target="_blank"
            rel="noreferrer"
            className="mt-1 inline-block text-xs text-sky-400 underline-offset-2 hover:text-sky-300 hover:underline"
          >
            {toast.action.label} ↗
          </a>
        )}
      </div>
      <button
        type="button"
        onClick={onDismiss}
        aria-label="dismiss notification"
        className="shrink-0 text-xs text-zinc-600 transition hover:text-zinc-300"
      >
        ✕
      </button>
    </div>
  );
}

function toneFor(kind: ToastKind): { border: string; icon: string } {
  switch (kind) {
    case "success":
      return { border: "border-emerald-700/60", icon: "text-emerald-400" };
    case "error":
      return { border: "border-red-700/60", icon: "text-red-400" };
    case "pending":
      return { border: "border-zinc-700", icon: "text-sky-300" };
    case "info":
    default:
      return { border: "border-zinc-700", icon: "text-zinc-300" };
  }
}

function ToastGlyph({ kind }: { kind: ToastKind }) {
  // Tiny inline glyphs so we don't need to drag in another icon import.
  const cls = "h-3 w-3";
  if (kind === "success") {
    return (
      <svg viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="2" className={cls} aria-hidden>
        <path d="M2.5 6.2 5 8.7l4.5-5" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    );
  }
  if (kind === "error") {
    return (
      <svg viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="2" className={cls} aria-hidden>
        <path d="M3 3l6 6M9 3l-6 6" strokeLinecap="round" />
      </svg>
    );
  }
  // info
  return (
    <svg viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="2" className={cls} aria-hidden>
      <circle cx="6" cy="6" r="4.5" />
      <path d="M6 5.5v3M6 3.5v.01" strokeLinecap="round" />
    </svg>
  );
}
