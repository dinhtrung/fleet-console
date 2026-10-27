/** The signature move: a magnet.
 *
 *  Status is a fixed-size, fixed-slot token that carries a three-letter code, a fill weight and a
 *  hue — so state survives greyscale, colour blindness and a screenshot. The tooltip and the
 *  accessible name always carry the word. */

import type { ReactNode } from "react";
import { IconCaretDownFilled, IconCaretUpFilled, IconMinus } from "@tabler/icons-react";
import { cn } from "../../lib/cn";

const STATUS: Record<string, { code: string; word: string; className: string }> = {
  active: { code: "ACT", word: "Active", className: "bg-plum text-on-plum" },
  invited: { code: "INV", word: "Invited", className: "bg-selected text-ink-muted" },
  suspended: { code: "SUS", word: "Suspended", className: "bg-attention text-on-attention" },
  deactivated: { code: "DEA", word: "Deactivated", className: "border border-rule-strong text-ink-muted" },
  trial: { code: "TRL", word: "Trial", className: "border border-rule-strong text-ink-muted" },
  past_due: { code: "DUE", word: "Past due", className: "bg-attention text-on-attention" },
  owner: { code: "OWN", word: "Owner", className: "bg-plum text-on-plum" },
  admin: { code: "ADM", word: "Admin", className: "border border-rule-strong text-ink" },
  member: { code: "MEM", word: "Member", className: "border border-rule text-ink-muted" },
  revoked: { code: "REV", word: "Revoked", className: "border border-rule-strong text-ink-muted" },
  on: { code: "ON", word: "On", className: "bg-plum text-on-plum" },
  off: { code: "OFF", word: "Off", className: "border border-rule-strong text-ink-muted" },
  gradual: { code: "GRD", word: "Gradual", className: "bg-selected text-ink-muted" },
};

export function StatusMagnet({
  status,
  className,
  title,
}: {
  status: string;
  className?: string;
  title?: string;
}) {
  const def = STATUS[status] ?? { code: status.slice(0, 3).toUpperCase(), word: status, className: "border border-rule text-ink-muted" };
  return (
    <span className={cn("magnet", def.className, className)} title={title ?? def.word} aria-label={def.word}>
      <span aria-hidden="true">{def.code}</span>
      <span className="sr-only">{def.word}</span>
    </span>
  );
}

/** Everything that is a chip but not a state: counts, plans, tags. Deliberately quieter than a
 *  magnet, so the state column stays the loudest thing on a row. */
export function Badge({
  children,
  tone = "quiet",
  className,
  mono = false,
  title,
}: {
  children: ReactNode;
  tone?: "quiet" | "ink" | "tint";
  className?: string;
  mono?: boolean;
  title?: string;
}) {
  const tones = {
    quiet: "border border-rule text-ink-muted",
    ink: "bg-ink text-ground",
    tint: "bg-selected text-ink-muted",
  } as const;
  return (
    <span
      className={cn(
        "inline-flex items-center whitespace-nowrap rounded-chip px-1.5 py-0.5 text-[0.6875rem] font-semibold",
        mono && "font-mono tracking-tight",
        tones[tone],
        className,
      )}
      title={title}
    >
      {children}
    </span>
  );
}

/** A count that has a direction. The arrow is drawn, not typed, and the number is always shown. */
export function Delta({ value, label }: { value: number; label: string }) {
  const tone = value === 0 ? "text-ink-muted" : "text-ink";
  return (
    <span className={cn("inline-flex items-center gap-1 text-[0.6875rem]", tone)}>
      {value === 0 ? (
        <IconMinus size={10} className="shrink-0" aria-hidden="true" />
      ) : value > 0 ? (
        <IconCaretUpFilled size={10} className="shrink-0" aria-hidden="true" />
      ) : (
        <IconCaretDownFilled size={10} className="shrink-0" aria-hidden="true" />
      )}
      <span className="num">
        {value > 0 ? "+" : value < 0 ? "−" : ""}
        {Math.abs(value)}
      </span>
      <span className="text-ink-muted">{label}</span>
    </span>
  );
}
