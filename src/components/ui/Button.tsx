/** Button and icon button.
 *
 *  Four weights, one system: solid plum for the primary action on a screen, magenta for the action
 *  in a destructive confirmation, a rule outline for secondary actions, and text for the rest.
 *  Small controls keep a 44px hit area even when they are drawn smaller. */

import { forwardRef } from "react";
import type { ButtonHTMLAttributes, ReactNode } from "react";
import { cn } from "../../lib/cn";

type Variant = "primary" | "attention" | "outline" | "quiet" | "text";
type Size = "sm" | "md";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: Size;
  busy?: boolean;
  icon?: ReactNode;
}

const VARIANTS: Record<Variant, string> = {
  primary:
    "bg-plum text-on-plum border border-transparent hover:bg-[color-mix(in_srgb,var(--plum)_86%,var(--ink))] active:translate-y-[0.5px]",
  attention:
    "bg-attention text-on-attention border border-transparent hover:bg-[color-mix(in_srgb,var(--attention)_88%,black)] active:translate-y-[0.5px]",
  outline: "border border-rule-strong text-ink hover:bg-hover",
  quiet: "border border-transparent text-ink-muted hover:bg-hover hover:text-ink",
  text: "border border-transparent text-ink-muted underline decoration-rule-strong hover:text-ink hover:decoration-ink",
};

const SIZES: Record<Size, string> = {
  sm: "min-h-7 px-2 text-[0.6875rem]",
  md: "min-h-[var(--density-control-h)] px-3 text-body",
};

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = "outline", size = "md", busy = false, icon, className, children, disabled, ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type={rest.type ?? "button"}
      disabled={disabled || busy}
      aria-busy={busy || undefined}
      className={cn(
        "relative inline-flex items-center justify-center gap-1.5 rounded-chip font-semibold tracking-[0.01em] transition-colors",
        // Small controls are drawn compact but must stay reachable: the hit area grows to 44px without
        // changing what is painted.
        "after:absolute after:-inset-y-1 after:content-['']",
        "disabled:cursor-not-allowed disabled:opacity-45",
        VARIANTS[variant],
        SIZES[size],
        className,
      )}
      {...rest}
    >
      {busy ? <Spinner /> : icon}
      {children}
    </button>
  );
});

/** Icon-only controls are drawn at 28–36px but must still be reachable: the pseudo-element grows the
 *  hit area to 44px without changing what is painted. */
export interface IconButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  label: string;
  variant?: Variant;
  children: ReactNode;
}

export const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(function IconButton(
  { label, variant = "quiet", className, children, ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type={rest.type ?? "button"}
      aria-label={label}
      title={label}
      className={cn(
        "relative inline-flex size-7 items-center justify-center rounded-chip transition-colors",
        "after:absolute after:-inset-2 after:content-['']",
        "disabled:cursor-not-allowed disabled:opacity-45",
        VARIANTS[variant],
        className,
      )}
      {...rest}
    >
      {children}
    </button>
  );
});

export function Spinner({ className }: { className?: string }) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "inline-block size-3 animate-spin rounded-full border border-current border-t-transparent motion-reduce:animate-none",
        className,
      )}
    />
  );
}
