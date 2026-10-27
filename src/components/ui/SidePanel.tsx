/** The side panel: a record editor that enters from the right edge.
 *
 *  At desktop sizes it covers at most half the viewport and is **not modal** — no backdrop, no focus
 *  trap — because the whole reason this pattern exists is that the operator is editing a record while
 *  watching the directory it belongs to. Below the narrow breakpoint there is no "beside": the panel
 *  takes the full width, becomes a modal overlay, and contains focus, so the keyboard cannot wander
 *  into a table the operator cannot see.
 *
 *  Either way: focus moves into the panel on open, Escape dismisses it, and focus returns to the
 *  control that opened it. */

import { useCallback, useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { X } from "lucide-react";
import { cn } from "../../lib/cn";
import { IconButton } from "./Button";

const FOCUSABLE =
  'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';

function useNarrow(breakpoint = 640): boolean {
  const [narrow, setNarrow] = useState(() =>
    typeof window === "undefined" ? false : window.innerWidth < breakpoint,
  );
  useEffect(() => {
    const media = window.matchMedia(`(max-width: ${breakpoint - 1}px)`);
    const listener = () => setNarrow(media.matches);
    listener();
    media.addEventListener("change", listener);
    return () => media.removeEventListener("change", listener);
  }, [breakpoint]);
  return narrow;
}

export interface SidePanelProps {
  open: boolean;
  onClose: () => void;
  title: string;
  description?: string;
  children: ReactNode;
  footer?: ReactNode;
  /** Called when the operator tries to dismiss; return false to keep the panel open. */
  onBeforeClose?: () => boolean;
}

export function SidePanel({ open, onClose, title, description, children, footer, onBeforeClose }: SidePanelProps) {
  const panel = useRef<HTMLDivElement>(null);
  const restoreTo = useRef<HTMLElement | null>(null);
  const narrow = useNarrow();

  const requestClose = useCallback(() => {
    if (onBeforeClose && onBeforeClose() === false) return;
    onClose();
  }, [onBeforeClose, onClose]);

  // Focus enters on open and returns to the trigger on close, in both modes.
  useEffect(() => {
    if (!open) return;
    restoreTo.current = document.activeElement as HTMLElement | null;
    const first = panel.current?.querySelector<HTMLElement>(FOCUSABLE);
    (first ?? panel.current)?.focus();
    return () => {
      if (restoreTo.current && document.contains(restoreTo.current)) restoreTo.current.focus();
    };
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.stopPropagation();
        requestClose();
        return;
      }
      // Focus is contained only in the modal (narrow) presentation.
      if (!narrow || event.key !== "Tab") return;
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
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, narrow, requestClose]);

  if (!open) return null;

  return (
    <div className={cn("fixed inset-0 z-50", narrow ? "flex" : "pointer-events-none flex justify-end")}>
      {narrow ? (
        <div
          className="pointer-events-auto absolute inset-0 bg-[color-mix(in_srgb,var(--palette-ink)_55%,transparent)]"
          onClick={requestClose}
          aria-hidden="true"
        />
      ) : null}
      <div
        ref={panel}
        role="dialog"
        aria-modal={narrow ? true : undefined}
        aria-labelledby="side-panel-title"
        aria-describedby={description ? "side-panel-description" : undefined}
        tabIndex={-1}
        className={cn(
          "pointer-events-auto relative flex h-full w-full flex-col border-l border-rule-strong bg-panel shadow-overlay",
          "sm:w-1/2 sm:max-w-[44rem]",
          "motion-safe:animate-[panel-in_220ms_cubic-bezier(0.16,1,0.3,1)]",
        )}
      >
        <div className="flex items-start gap-2 border-b border-rule px-3 py-2">
          <div className="min-w-0">
            <h2 id="side-panel-title" className="text-lead font-semibold tracking-[-0.01em] text-ink">
              {title}
            </h2>
            {description ? (
              <p id="side-panel-description" className="mt-0.5 text-[0.6875rem] text-ink-muted">
                {description}
              </p>
            ) : null}
          </div>
          <IconButton label="Close panel" onClick={requestClose} className="ml-auto shrink-0">
            <X size={14} />
          </IconButton>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3">{children}</div>
        {footer ? (
          <div className="flex flex-wrap items-center justify-end gap-2 border-t border-rule px-3 py-2">{footer}</div>
        ) : null}
      </div>
    </div>
  );
}
