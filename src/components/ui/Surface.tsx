/** Surface primitives: the board is ruled, not shadowed. One panel shape, one rule weight, one
 *  toolbar row — so a screen is assembled from the same three things everywhere. */

import type { ReactNode } from "react";
import { cn } from "../../lib/cn";

export function Panel({
  children,
  className,
  as: As = "section",
}: {
  children: ReactNode;
  className?: string;
  as?: "section" | "div" | "article";
}) {
  return <As className={cn("border border-rule bg-panel", className)}>{children}</As>;
}

export function PanelHead({
  title,
  count,
  actions,
  description,
  className,
}: {
  title: string;
  count?: ReactNode;
  actions?: ReactNode;
  description?: string;
  className?: string;
}) {
  return (
    <div className={cn("flex flex-wrap items-baseline gap-x-3 gap-y-2 border-b border-rule px-3 py-2", className)}>
      <h2 className="text-label text-ink-muted">{title}</h2>
      {count !== undefined ? <span className="text-[0.6875rem] text-ink-muted num">{count}</span> : null}
      {description ? <p className="w-full text-[0.6875rem] text-ink-muted sm:w-auto">{description}</p> : null}
      {actions ? <div className="ml-auto flex items-center gap-1.5">{actions}</div> : null}
    </div>
  );
}

export function Toolbar({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div className={cn("flex flex-wrap items-end gap-2 border-b border-rule px-3 py-2", className)}>{children}</div>
  );
}

export function Rule({ className }: { className?: string }) {
  return <hr className={cn("border-t border-rule", className)} />;
}

/** A definition row: label above value, used wherever the panel shows one record's fields. */
export function FieldRow({
  label,
  children,
  className,
}: {
  label: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex flex-col gap-0.5 py-1.5", className)}>
      <span className="label text-ink-muted">{label}</span>
      <span className="text-body text-ink">{children}</span>
    </div>
  );
}
