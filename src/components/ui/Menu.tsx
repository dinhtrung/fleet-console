/** Menu: a small action list for one row or one corner of the chrome. Keyboard-operable
 *  (Arrow keys, Home/End, Escape), closes on outside click, and returns focus to its trigger. */

import { useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { IconCheck } from "@tabler/icons-react";
import { cn } from "../../lib/cn";
import { IconButton } from "./Button";

export interface MenuItem {
  id: string;
  label: string;
  hint?: string;
  destructive?: boolean;
  disabled?: boolean;
  /** A leading swatch — used by the appearance switcher to preview each appearance's own colours. */
  swatch?: ReactNode;
  /** Present on a mutually-exclusive list: the items become radios and this marks the active one. */
  checked?: boolean;
  onSelect: () => void;
}

export function Menu({
  label,
  trigger,
  items,
  align = "end",
  triggerClassName,
  triggerVariant = "icon",
  placement = "below",
}: {
  label: string;
  trigger: ReactNode;
  items: MenuItem[];
  align?: "start" | "end";
  triggerClassName?: string;
  /** "icon" for a compact corner control, "text" for a labelled trigger such as the appearance
   *  switcher, which has to show the current choice rather than an icon. */
  triggerVariant?: "icon" | "text";
  /** "above" for a trigger sitting at the bottom of the viewport, such as the rail's switcher. */
  placement?: "below" | "above";
}) {
  const [open, setOpen] = useState(false);
  const wrap = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const isRadioList = items.some((item) => item.checked !== undefined);
  const itemRole = isRadioList ? "menuitemradio" : "menuitem";

  useEffect(() => {
    if (!open) return;
    const onDown = (event: PointerEvent) => {
      if (!wrap.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", onDown);
    return () => document.removeEventListener("pointerdown", onDown);
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const first = listRef.current?.querySelector<HTMLElement>('[role="menuitem"]:not([aria-disabled="true"]), [role="menuitemradio"]:not([aria-disabled="true"])');
    first?.focus();
  }, [open]);

  const close = (returnFocus = true) => {
    setOpen(false);
    if (returnFocus) triggerRef.current?.focus();
  };

  const onListKeyDown = (event: React.KeyboardEvent) => {
    const nodes = Array.from(
      listRef.current?.querySelectorAll<HTMLElement>(
        '[role="menuitem"]:not([aria-disabled="true"]), [role="menuitemradio"]:not([aria-disabled="true"])',
      ) ?? [],
    );
    const index = nodes.indexOf(document.activeElement as HTMLElement);
    if (event.key === "Escape") {
      event.preventDefault();
      close();
    } else if (event.key === "ArrowDown") {
      event.preventDefault();
      nodes[(index + 1) % nodes.length]?.focus();
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      nodes[(index - 1 + nodes.length) % nodes.length]?.focus();
    } else if (event.key === "Home") {
      event.preventDefault();
      nodes[0]?.focus();
    } else if (event.key === "End") {
      event.preventDefault();
      nodes[nodes.length - 1]?.focus();
    }
  };

  return (
    <div ref={wrap} className="relative">
      {triggerVariant === "text" ? (
        <button
          ref={triggerRef}
          type="button"
          aria-haspopup="menu"
          aria-expanded={open}
          aria-label={label}
          onClick={() => setOpen((v) => !v)}
          className={triggerClassName}
        >
          {trigger}
        </button>
      ) : (
        <IconButton
          ref={triggerRef}
          label={label}
          aria-haspopup="menu"
          aria-expanded={open}
          onClick={() => setOpen((v) => !v)}
          className={triggerClassName}
        >
          {trigger}
        </IconButton>
      )}
      {open ? (
        <div
          ref={listRef}
          role="menu"
          aria-label={label}
          onKeyDown={onListKeyDown}
          className={cn(
            "absolute z-40 w-max min-w-56 border border-rule-strong bg-panel py-1 shadow-overlay",
            placement === "above" ? "bottom-full mb-1" : "mt-1",
            align === "end" ? "right-0" : "left-0",
          )}
        >
          {items.map((item) => (
            <button
              key={item.id}
              role={itemRole}
              type="button"
              aria-checked={isRadioList ? Boolean(item.checked) : undefined}
              aria-disabled={item.disabled || undefined}
              disabled={item.disabled}
              onClick={() => {
                item.onSelect();
                close(true);
              }}
              className={cn(
                "flex w-full items-center gap-2 px-3 py-1.5 text-left text-body",
                "hover:bg-hover disabled:cursor-not-allowed disabled:opacity-45",
                item.destructive ? "text-attention font-semibold" : "text-ink",
              )}
            >
              {item.swatch ? <span className="shrink-0">{item.swatch}</span> : null}
              <span>{item.label}</span>
              {item.hint ? <span className="ml-auto text-[0.6875rem] text-ink-muted">{item.hint}</span> : null}
              {isRadioList ? (
                <span className="ml-auto text-ink" aria-hidden="true">
                  {item.checked ? <IconCheck size={13} /> : null}
                </span>
              ) : null}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}
