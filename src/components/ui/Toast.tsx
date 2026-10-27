/** Toasts: the receipt for an action the operator just took. Announced politely, dismissible, and
 *  never the only place a result appears — the board itself changes too. */

import { createContext, useCallback, useContext, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { IconAlertTriangle, IconCheck, IconX } from "@tabler/icons-react";
import { cn } from "../../lib/cn";

type Tone = "done" | "problem";

interface Toast {
  id: number;
  tone: Tone;
  message: string;
  detail?: string;
}

interface ToastApi {
  done: (message: string, detail?: string) => void;
  problem: (message: string, detail?: string) => void;
}

const ToastContext = createContext<ToastApi | null>(null);

let seq = 0;

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);

  const push = useCallback((tone: Tone, message: string, detail?: string) => {
    seq += 1;
    const id = seq;
    setToasts((current) => [...current, { id, tone, message, detail }]);
    window.setTimeout(() => setToasts((current) => current.filter((t) => t.id !== id)), 6000);
  }, []);

  const api = useMemo<ToastApi>(
    () => ({
      done: (message, detail) => push("done", message, detail),
      problem: (message, detail) => push("problem", message, detail),
    }),
    [push],
  );

  return (
    <ToastContext.Provider value={api}>
      {children}
      <div
        role="status"
        aria-live="polite"
        aria-atomic="false"
        className="pointer-events-none fixed inset-x-3 bottom-3 z-50 flex flex-col items-end gap-2 sm:inset-x-auto sm:right-4 sm:bottom-4"
      >
        {toasts.map((toast) => (
          <div
            key={toast.id}
            className={cn(
              "pointer-events-auto flex w-full max-w-sm items-start gap-2 border bg-panel px-3 py-2 shadow-overlay",
              toast.tone === "problem" ? "border-attention" : "border-rule-strong",
            )}
          >
            <span className="mt-0.5 shrink-0" aria-hidden="true">
              {toast.tone === "problem" ? (
                <IconAlertTriangle size={12} className="text-attention" />
              ) : (
                <IconCheck size={12} className="text-ink" />
              )}
            </span>
            <div className="min-w-0">
              <p className="text-body text-ink">{toast.message}</p>
              {toast.detail ? <p className="mt-0.5 text-[0.6875rem] text-ink-muted">{toast.detail}</p> : null}
            </div>
            <button
              type="button"
              onClick={() => setToasts((current) => current.filter((t) => t.id !== toast.id))}
              className="relative ml-auto shrink-0 text-ink-muted hover:text-ink after:absolute after:-inset-3 after:content-['']"
              aria-label="Dismiss"
            >
              <IconX size={10} />
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast(): ToastApi {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error("useToast must be used inside ToastProvider");
  return ctx;
}
