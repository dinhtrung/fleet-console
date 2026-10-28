/** Button and icon button — the shadcn button, carrying the board's four weights.
 *
 *  Generated from the shadcn Base UI registry, then fitted to `DESIGN.md`: square corners, one
 *  border, no elevation. Colours resolve through the shadcn semantic variables (`--primary`,
 *  `--foreground`, `--border`, `--ring`) that `src/index.css` maps onto the sage roles, so this file
 *  carries no palette value. Small controls keep a 44px hit area even when drawn smaller. */

import { forwardRef } from "react";
import type { ComponentPropsWithoutRef, ReactNode } from "react";
import { Button as ButtonPrimitive } from "@base-ui/react/button";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const buttonVariants = cva(
  "group/button relative inline-flex shrink-0 items-center justify-center gap-1.5 rounded-chip border font-semibold tracking-[0.01em] whitespace-nowrap transition-colors outline-none after:absolute after:-inset-y-1 after:content-[''] disabled:cursor-not-allowed disabled:opacity-45 [&_svg]:pointer-events-none [&_svg]:shrink-0",
  {
    variants: {
      variant: {
        primary:
          "border-transparent bg-primary text-primary-foreground hover:bg-[color-mix(in_srgb,var(--primary)_86%,var(--foreground))] active:translate-y-[0.5px]",
        attention:
          "border-transparent bg-destructive text-on-attention hover:bg-[color-mix(in_srgb,var(--destructive)_88%,black)] active:translate-y-[0.5px]",
        outline: "border-rule-strong text-ink hover:bg-hover",
        quiet: "border-transparent text-ink-muted hover:bg-hover hover:text-ink",
        text: "border-transparent text-ink-muted underline decoration-rule-strong hover:text-ink hover:decoration-ink",
      },
      size: {
        sm: "min-h-7 px-2 text-[0.6875rem]",
        md: "min-h-[var(--density-control-h)] px-3 text-body",
      },
    },
    defaultVariants: {
      variant: "outline",
      size: "md",
    },
  },
);

export interface ButtonProps
  extends ComponentPropsWithoutRef<typeof ButtonPrimitive>,
    VariantProps<typeof buttonVariants> {
  busy?: boolean;
  icon?: ReactNode;
}

export const Button = forwardRef<HTMLElement, ButtonProps>(function Button(
  { className, variant, size, busy = false, icon, children, disabled, type, ...rest },
  ref,
) {
  return (
    <ButtonPrimitive
      ref={ref}
      data-slot="button"
      type={type ?? "button"}
      disabled={disabled || busy}
      aria-busy={busy || undefined}
      className={cn(buttonVariants({ variant, size, className }))}
      {...rest}
    >
      {busy ? <Spinner /> : icon}
      {children}
    </ButtonPrimitive>
  );
});

/** Icon-only controls are drawn at 28–36px but must still be reachable: the pseudo-element grows the
 *  hit area to 44px without changing what is painted. */
export interface IconButtonProps extends ComponentPropsWithoutRef<typeof ButtonPrimitive> {
  label: string;
  variant?: NonNullable<VariantProps<typeof buttonVariants>["variant"]>;
  children: ReactNode;
}

export const IconButton = forwardRef<HTMLElement, IconButtonProps>(function IconButton(
  { label, variant = "quiet", className, children, type, ...rest },
  ref,
) {
  return (
    <ButtonPrimitive
      ref={ref}
      data-slot="icon-button"
      type={type ?? "button"}
      aria-label={label}
      title={label}
      className={cn(
        "relative inline-flex size-7 items-center justify-center rounded-chip border transition-colors",
        "after:absolute after:-inset-2 after:content-['']",
        "disabled:cursor-not-allowed disabled:opacity-45",
        buttonVariants({ variant, size: "md" }),
        "min-h-0 px-0",
        className,
      )}
      {...rest}
    >
      {children}
    </ButtonPrimitive>
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
