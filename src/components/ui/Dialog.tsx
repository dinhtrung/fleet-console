/** Dialog and the destructive confirmation.
 *
 *  A modal is only used where the task genuinely needs protected focus: a destructive or
 *  irreversible action, or a form that must not be lost. Focus moves in, is contained, returns to
 *  the control that opened it, and Escape cancels. */

import { useCallback, useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { cn } from "../../lib/cn";
import { Button } from "./Button";

const FOCUSABLE =
  'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';

export function Dialog({
  open,
  onClose,
  title,
  description,
  children,
  footer,
  width = "md",
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  description?: string;
  children?: ReactNode;
  footer?: ReactNode;
  width?: "sm" | "md" | "lg";
}) {
  const panel = useRef<HTMLDivElement>(null);
  const restoreTo = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (!open) return;
    restoreTo.current = document.activeElement as HTMLElement | null;
    const node = panel.current;
    const first = node?.querySelector<HTMLElement>(FOCUSABLE);
    (first ?? node)?.focus();
    return () => restoreTo.current?.focus?.();
  }, [open]);

  const onKeyDown = useCallback(
    (event: React.KeyboardEvent) => {
      if (event.key === "Escape") {
        event.stopPropagation();
        onClose();
        return;
      }
      if (event.key !== "Tab") return;
      const node = panel.current;
      if (!node) return;
      const items = Array.from(node.querySelectorAll<HTMLElement>(FOCUSABLE)).filter((el) => el.offsetParent !== null);
      if (items.length === 0) return;
      const first = items[0]!;
      const last = items[items.length - 1]!;
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    },
    [onClose],
  );

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto p-4 sm:items-center">
      <div className="fixed inset-0 bg-[color-mix(in_srgb,var(--palette-ink)_55%,transparent)]" onClick={onClose} aria-hidden="true" />
      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-labelledby="dialog-title"
        aria-describedby={description ? "dialog-description" : undefined}
        tabIndex={-1}
        onKeyDown={onKeyDown}
        className={cn(
          "relative z-10 my-auto w-full border border-rule-strong bg-panel shadow-overlay",
          width === "sm" && "max-w-sm",
          width === "md" && "max-w-lg",
          width === "lg" && "max-w-2xl",
        )}
      >
        <div className="border-b border-rule px-3 py-2">
          <h2 id="dialog-title" className="text-lead font-semibold tracking-[-0.01em] text-ink">
            {title}
          </h2>
          {description ? (
            <p id="dialog-description" className="mt-1 text-[0.6875rem] text-ink-muted">
              {description}
            </p>
          ) : null}
        </div>
        {children ? <div className="px-3 py-3">{children}</div> : null}
        {footer ? <div className="flex items-center justify-end gap-2 border-t border-rule px-3 py-2">{footer}</div> : null}
      </div>
    </div>
  );
}

/** Confirmation for a destructive action.
 *
 *  The dangerous button is never the default: it is magenta, it is on the right, and where the
 *  action cannot be undone the operator types the target's name before it enables. */
export function ConfirmDialog({
  open,
  onClose,
  onConfirm,
  title,
  description,
  confirmLabel,
  requireTyped,
  typedLabel,
  busy = false,
  error,
}: {
  open: boolean;
  onClose: () => void;
  onConfirm: () => void;
  title: string;
  description: string;
  confirmLabel: string;
  requireTyped?: string;
  typedLabel?: string;
  busy?: boolean;
  error?: string;
}) {
  const [typed, setTyped] = useState("");
  const armed = !requireTyped || typed.trim() === requireTyped;
  // The typed confirmation is cleared where the dialog closes, not in an effect watching `open`.
  const close = () => {
    setTyped("");
    onClose();
  };

  return (
    <Dialog
      open={open}
      onClose={close}
      title={title}
      description={description}
      width="sm"
      footer={
        <>
          <Button variant="quiet" onClick={close}>
            Cancel
          </Button>
          <Button variant="attention" onClick={onConfirm} disabled={!armed} busy={busy}>
            {confirmLabel}
          </Button>
        </>
      }
    >
      {requireTyped ? (
        <label className="flex flex-col gap-1">
          <span className="text-[0.6875rem] text-ink-muted">
            {typedLabel ?? "Type the name to confirm"}
          </span>
          <input
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            placeholder={requireTyped}
            className="min-h-9 w-full rounded-chip border border-rule-strong bg-panel px-2 text-body text-ink"
          />
        </label>
      ) : null}
      {error ? <p className="mt-2 text-[0.6875rem] font-semibold text-attention">{error}</p> : null}
    </Dialog>
  );
}
