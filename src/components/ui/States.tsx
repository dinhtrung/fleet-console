/** The five states every surface owes the operator: loading, empty, no matches, failed, denied.
 *  A screen that renders only its happy path is unfinished, so these are shared rather than
 *  re-invented per screen. */

import type { ReactNode } from "react";
import { cn } from "../../lib/cn";
import { Button } from "./Button";

export function LoadingRows({ rows = 6, className }: { rows?: number; className?: string }) {
  return (
    <div className={cn("divide-y divide-[var(--rule)]", className)} aria-busy="true" aria-live="polite">
      <span className="sr-only">Loading records</span>
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="flex items-center gap-3 px-3 py-[var(--density-row-pad-y)]">
          <span className="h-3 w-1/4 rounded-chip bg-hover" />
          <span className="h-3 w-1/6 rounded-chip bg-hover" />
          <span className="ml-auto h-3 w-16 rounded-chip bg-hover" />
        </div>
      ))}
    </div>
  );
}

export function EmptyState({
  title,
  body,
  action,
}: {
  title: string;
  body: string;
  action?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-start gap-2 px-3 py-8">
      <p className="text-lead font-semibold text-ink">{title}</p>
      <p className="max-w-[60ch] text-body text-ink-muted">{body}</p>
      {action}
    </div>
  );
}

export function NoMatchesState({ query, onClear }: { query: string; onClear: () => void }) {
  return (
    <div className="flex flex-col items-start gap-2 px-3 py-8">
      <p className="text-lead font-semibold text-ink">Nothing on the board matches that</p>
      <p className="max-w-[60ch] text-body text-ink-muted">
        No record matches <span className="font-mono text-ink">{query}</span> with the current filters. The board is not
        empty — clear the filters to see everything again.
      </p>
      <Button variant="outline" size="sm" onClick={onClear}>
        Clear filters
      </Button>
    </div>
  );
}

export function ErrorState({
  title = "The board did not load",
  error,
  onRetry,
}: {
  title?: string;
  error: unknown;
  onRetry: () => void;
}) {
  const message = error instanceof Error ? error.message : "Something went wrong.";
  return (
    <div className="flex flex-col items-start gap-2 border-l-0 px-3 py-8">
      <p className="text-lead font-semibold text-ink">{title}</p>
      <p className="max-w-[60ch] text-body text-ink-muted">{message}</p>
      <p className="max-w-[60ch] text-[0.6875rem] text-ink-muted">
        Nothing was changed. Retrying asks the service for the same view again.
      </p>
      <Button variant="outline" size="sm" onClick={onRetry}>
        Try again
      </Button>
    </div>
  );
}

export function DeniedState({
  permission,
  what,
}: {
  permission: string;
  what: string;
}) {
  return (
    <div className="flex flex-col items-start gap-2 px-3 py-8">
      <p className="text-lead font-semibold text-ink">Not your board</p>
      <p className="max-w-[60ch] text-body text-ink-muted">
        {what} needs the <span className="font-mono text-ink">{permission}</span> permission, which
        your roles do not grant. Nothing is broken and nothing is hidden from a mistake — ask an owner
        if you need it.
      </p>
    </div>
  );
}

export function NotFoundState({ what, children }: { what: string; children?: ReactNode }) {
  return (
    <div className="flex flex-col items-start gap-2 px-3 py-8">
      <p className="text-lead font-semibold text-ink">No such {what}</p>
      <p className="max-w-[60ch] text-body text-ink-muted">
        This address points at a {what} that is not on the board. It may have been removed, or the
        link may be from a different board.
      </p>
      {children}
    </div>
  );
}
