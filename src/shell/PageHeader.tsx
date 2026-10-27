/** Page header: where am I, what is this surface, and what can I do here.
 *  The breadcrumb is part of the board's grammar — a ruled line always sits under it. */

import type { ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { ChevronRight } from "lucide-react";

export function PageHeader({
  title,
  mark,
  count,
  description,
  crumbs = [],
  actions,
}: {
  title: string;
  /** A mark that belongs to the subject of the page — an organization's logo, say. Purely
   *  decorative: the title next to it already names the thing, so it carries no accessible text. */
  mark?: ReactNode;
  count?: ReactNode;
  description?: string;
  crumbs?: { label: string; to?: string }[];
  actions?: ReactNode;
}) {
  return (
    <header className="border-b border-rule bg-ground px-3 pt-3 pb-2 sm:px-5">
      {crumbs.length > 0 ? (
        <nav aria-label="Breadcrumb" className="mb-2">
          <ol className="flex flex-wrap items-center gap-1 text-[0.6875rem] text-ink-muted">
            {crumbs.map((crumb, i) => (
              <li key={`${crumb.label}-${i}`} className="flex items-center gap-1">
                {i > 0 ? <ChevronRight size={11} aria-hidden="true" className="text-ink-muted" /> : null}
                {crumb.to ? (
                  <Link to={crumb.to} className="hover:text-ink">
                    {crumb.label}
                  </Link>
                ) : (
                  <span aria-current={i === crumbs.length - 1 ? "page" : undefined} className="text-ink">
                    {crumb.label}
                  </span>
                )}
              </li>
            ))}
          </ol>
        </nav>
      ) : null}
      <div className="flex flex-wrap items-end gap-x-3 gap-y-2">
        {mark ? <span className="mb-0.5 shrink-0">{mark}</span> : null}
        <h1 className="text-title font-semibold tracking-[-0.015em] text-ink">{title}</h1>
        {count !== undefined ? <span className="num pb-1 text-[0.6875rem] text-ink-muted">{count}</span> : null}
        {actions ? <div className="ml-auto flex flex-wrap items-center gap-1.5">{actions}</div> : null}
      </div>
      {description ? <p className="mt-1.5 max-w-[75ch] text-body text-ink-muted">{description}</p> : null}
    </header>
  );
}
