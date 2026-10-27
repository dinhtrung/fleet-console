/** The shared grid: every list in the panel is this component.
 *
 *  It owns the behaviours the frozen baseline specifies for a list — announced sort state,
 *  pagination and page size, column control, selection with a bulk bar, and four distinct
 *  presentations (loading, empty board, no matches, failed request) — so no screen invents its own.
 *  Sorting is real: `aria-sort` follows the comparator, and clicking a header again reverses both
 *  the order and the announcement. */

import { useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { ArrowDown, ArrowUp, ChevronLeft, ChevronRight, Columns3 } from "lucide-react";
import { cn } from "../../lib/cn";
import { EmptyState, ErrorState, IconButton, LoadingRows, NoMatchesState } from "../ui";
import { Menu } from "../ui/Menu";
import { PAGE_SIZES } from "../../mock/api";

export interface GridColumn<T> {
  id: string;
  header: string;
  sortable?: boolean;
  align?: "left" | "right";
  width?: string;
  cell: (row: T) => ReactNode;
  /** Columns that are noise by default but must be reachable. */
  defaultHidden?: boolean;
  /** Identity columns are never hidden: a row with no name is not a row. */
  hideable?: boolean;
}

export interface DataGridProps<T> {
  rows: T[];
  columns: GridColumn<T>[];
  rowKey: (row: T) => string;
  caption: string;
  total: number;
  page: number;
  pageSize: number;
  sort?: string;
  dir: "asc" | "desc";
  onSort: (columnId: string) => void;
  onPage: (page: number) => void;
  onPageSize: (pageSize: number) => void;
  onRowClick?: (row: T) => void;
  selectable?: boolean;
  selected?: string[];
  onSelectedChange?: (ids: string[]) => void;
  bulkBar?: (selectedIds: string[], clear: () => void) => ReactNode;
  isLoading?: boolean;
  error?: unknown;
  onRetry?: () => void;
  emptyTitle?: string;
  emptyBody?: string;
  emptyAction?: ReactNode;
  noMatchesQuery?: string;
  onClearFilters?: () => void;
  /** Changes when the board is re-scoped, which runs the handover sweep once. */
  sweepKey?: number;
}

export function DataGrid<T>({
  rows,
  columns,
  rowKey,
  caption,
  total,
  page,
  pageSize,
  sort,
  dir,
  onSort,
  onPage,
  onPageSize,
  onRowClick,
  selectable = false,
  selected = [],
  onSelectedChange,
  bulkBar,
  isLoading = false,
  error,
  onRetry,
  emptyTitle = "Nothing on the board yet",
  emptyBody = "This list is empty. Once there are records they will appear here, and every column stays readable while it is empty.",
  emptyAction,
  noMatchesQuery,
  onClearFilters,
  sweepKey = 0,
}: DataGridProps<T>) {
  const [hidden, setHidden] = useState<string[]>(() => columns.filter((c) => c.defaultHidden).map((c) => c.id));
  const headCheck = useRef<HTMLInputElement>(null);

  const visible = useMemo(() => columns.filter((c) => !hidden.includes(c.id)), [columns, hidden]);
  const pageIds = rows.map(rowKey);
  const selectedOnPage = pageIds.filter((id) => selected.includes(id));
  const allOnPage = rows.length > 0 && selectedOnPage.length === rows.length;
  const someOnPage = selectedOnPage.length > 0 && !allOnPage;

  // The header checkbox is a three-state control, and `indeterminate` has no HTML attribute: it is
  // set on the node after render rather than during it.
  useEffect(() => {
    if (headCheck.current) headCheck.current.indeterminate = someOnPage;
  }, [someOnPage, rows.length, page]);

  const first = total === 0 ? 0 : (page - 1) * pageSize + 1;
  const last = Math.min(page * pageSize, total);
  const pages = Math.max(1, Math.ceil(total / pageSize));

  const toggleSort = (columnId: string) => onSort(columnId);

  return (
    <div className="flex min-w-0 flex-col">
      <div className="flex flex-wrap items-center gap-2 border-b border-rule px-3 py-1.5">
        <p className="text-[0.6875rem] text-ink-muted">
          <span className="num font-semibold text-ink">{total.toLocaleString("en-GB")}</span>{" "}
          {total === 1 ? "record" : "records"}
        </p>
        <div className="ml-auto flex items-center gap-1.5">
          <Menu
            label="Choose columns"
            align="end"
            trigger={<Columns3 size={14} />}
            items={columns
              .filter((c) => c.hideable !== false)
              .map((c) => ({
                id: c.id,
                label: `${hidden.includes(c.id) ? "Show" : "Hide"} ${c.header}`,
                onSelect: () =>
                  setHidden((current) => (current.includes(c.id) ? current.filter((id) => id !== c.id) : [...current, c.id])),
              }))}
          />
        </div>
      </div>

      {selectable && selected.length > 0 && bulkBar ? (
        <div
          role="region"
          aria-label="Bulk actions"
          className="flex flex-wrap items-center gap-2 border-b border-rule bg-selected px-3 py-1.5"
        >
          <p className="text-body text-ink">
            <span className="num font-semibold">{selected.length}</span> selected
          </p>
          <div className="ml-auto flex flex-wrap items-center gap-1.5">{bulkBar(selected, () => onSelectedChange?.([]))}</div>
        </div>
      ) : null}

      {isLoading ? (
        <LoadingRows rows={Math.min(pageSize, 8)} />
      ) : error ? (
        <ErrorState error={error} onRetry={() => onRetry?.()} />
      ) : rows.length === 0 ? (
        noMatchesQuery ? (
          <NoMatchesState query={noMatchesQuery} onClear={() => onClearFilters?.()} />
        ) : (
          <EmptyState title={emptyTitle} body={emptyBody} action={emptyAction} />
        )
      ) : (
        <div className="min-w-0 overflow-x-auto">
          <table className="w-full min-w-[46rem] border-collapse text-left">
            <caption className="sr-only">{caption}</caption>
            <thead>
              <tr className="border-b border-rule-strong">
                {selectable ? (
                  <th scope="col" className="w-9 px-3 py-1.5">
                    <input
                      ref={headCheck}
                      type="checkbox"
                      aria-label={allOnPage ? "Clear selection on this page" : "Select every row on this page"}
                      checked={allOnPage}
                      onChange={(e) => {
                        const next = e.target.checked
                          ? [...new Set([...selected, ...pageIds])]
                          : selected.filter((id) => !pageIds.includes(id));
                        onSelectedChange?.(next);
                      }}
                      className="size-4 accent-[var(--plum)]"
                    />
                  </th>
                ) : null}
                {visible.map((column) => {
                  const isSorted = sort === column.id;
                  return (
                    <th
                      key={column.id}
                      scope="col"
                      style={column.width ? { width: column.width } : undefined}
                      aria-sort={isSorted ? (dir === "asc" ? "ascending" : "descending") : "none"}
                      className={cn("px-3 py-1.5 align-bottom", column.align === "right" && "text-right")}
                    >
                      {column.sortable ? (
                        <button
                          type="button"
                          onClick={() => toggleSort(column.id)}
                          className={cn(
                            "label inline-flex items-center gap-1 transition-colors",
                            isSorted ? "text-ink" : "text-ink-muted hover:text-ink",
                          )}
                        >
                          {column.header}
                          <span aria-hidden="true" className={cn("transition-opacity", isSorted ? "opacity-100" : "opacity-35")}>
                            {isSorted && dir === "desc" ? <ArrowDown size={11} /> : <ArrowUp size={11} />}
                          </span>
                        </button>
                      ) : (
                        <span className="label text-ink-muted">{column.header}</span>
                      )}
                    </th>
                  );
                })}
              </tr>
            </thead>
            <tbody>
              {rows.map((row, index) => {
                const id = rowKey(row);
                const isSelected = selected.includes(id);
                return (
                  <tr
                    key={`${sweepKey}-${id}`}
                    className={cn(
                      "sweep border-b border-rule last:border-b-0",
                      onRowClick && "cursor-pointer",
                      isSelected ? "bg-selected" : "hover:bg-hover",
                    )}
                    style={{ ["--sweep-index" as string]: String(Math.min(index, 12)) }}
                  >
                    {selectable ? (
                      <td className="px-3 py-[var(--density-row-pad-y)]">
                        <input
                          type="checkbox"
                          aria-label={`Select ${id}`}
                          checked={isSelected}
                          onChange={(e) => {
                            onSelectedChange?.(
                              e.target.checked ? [...selected, id] : selected.filter((s) => s !== id),
                            );
                          }}
                          onClick={(e) => e.stopPropagation()}
                          className="size-4 accent-[var(--plum)]"
                        />
                      </td>
                    ) : null}
                    {visible.map((column, columnIndex) => (
                      <td
                        key={column.id}
                        className={cn(
                          "px-3 py-[var(--density-row-pad-y)] text-body text-ink align-middle",
                          column.align === "right" && "text-right",
                        )}
                        onClick={onRowClick && columnIndex === 0 ? () => onRowClick(row) : undefined}
                      >
                        {column.cell(row)}
                      </td>
                    ))}
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      <div className="flex flex-wrap items-center gap-2 border-t border-rule px-3 py-1.5">
        <p className="text-[0.6875rem] text-ink-muted">
          {total === 0 ? "No records" : `Records ${first}–${last} of ${total}`}
        </p>
        <label className="ml-auto flex items-center gap-1.5 text-[0.6875rem] text-ink-muted">
          Rows
          <select
            value={pageSize}
            onChange={(e) => onPageSize(Number(e.target.value))}
            className="min-h-7 rounded-chip border border-rule-strong bg-panel px-1 text-[0.6875rem] font-semibold text-ink"
          >
            {PAGE_SIZES.map((size) => (
              <option key={size} value={size}>
                {size}
              </option>
            ))}
          </select>
        </label>
        <div className="flex items-center gap-1">
          <IconButton
            label="Previous page"
            disabled={page <= 1}
            onClick={() => onPage(page - 1)}
            className="border border-rule-strong"
          >
            <ChevronLeft size={14} />
          </IconButton>
          <span className="px-1 text-[0.6875rem] text-ink-muted num">
            {page} / {pages}
          </span>
          <IconButton
            label="Next page"
            disabled={page >= pages}
            onClick={() => onPage(page + 1)}
            className="border border-rule-strong"
          >
            <ChevronRight size={14} />
          </IconButton>
        </div>
      </div>
    </div>
  );
}

/** Toolbar field: the one search input in the system. */
export function GridSearch({
  value,
  onChange,
  placeholder,
  label = "Search",
}: {
  value: string;
  onChange: (next: string) => void;
  placeholder: string;
  label?: string;
}) {
  return (
    <div className="flex min-w-[12rem] flex-1 flex-col gap-1">
      <label htmlFor="grid-search" className="label text-ink-muted">
        {label}
      </label>
      <input
        id="grid-search"
        type="search"
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
        className="min-h-9 w-full rounded-chip border border-rule-strong bg-panel px-2 text-body text-ink"
      />
    </div>
  );
}
