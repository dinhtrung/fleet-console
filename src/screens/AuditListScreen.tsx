/** The audit record: the app's own event log, read newest first.
 *
 *  Every row is one thing the app did — actor, kind, subject and detail — appended once and never
 *  edited. The server owns the paging: the screen asks for a page, then asks for the page before the
 *  cursor it was given. Filters are a closed set (`actor`, `kind`, `subject`, `since`, `until`), all
 *  applied by the server, so a filter this screen cannot express is one the operator cannot ask for.
 *
 *  Three states are kept distinct on purpose: loading, "no events match", and "the log could not be
 *  read" with a retry. A failed read is never rendered as an empty table — that would read as quiet,
 *  which is the one thing a broken audit log must not look like. */

import { useCallback, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { Link } from "@tanstack/react-router";
import { Button, EmptyState, ErrorState, IconButton, LoadingRows, NoMatchesState, TextInput, Toolbar } from "../components/ui";
import { PageHeader } from "../shell/PageHeader";
import { listEvents, type AuditEvent } from "../lib/fleet";
import { useListState } from "../lib/urlState";
import { dateTime, orDash, relativeTime } from "../lib/format";

/** The page size the server clamps to anyway; the screen asks for the same default so the pager
 *  arithmetic and the server's page agree. */
const PAGE_SIZE = 50;

const COLUMNS = ["When", "Actor", "Kind", "Subject", "Detail"] as const;

export function AuditListScreen() {
  return <AuditBoard />;
}

function AuditBoard() {
  const [list, setList] = useListState({
    actor: "",
    kind: "",
    subject: "",
    since: "",
    until: "",
    limit: PAGE_SIZE,
    // `before` is the keyset cursor: 0 means "the newest page". It lives in the URL so a page is
    // shareable and a reload lands on the same rows.
    before: 0,
  });

  const actor = typeof list.actor === "string" ? list.actor : "";
  const kind = typeof list.kind === "string" ? list.kind : "";
  const subject = typeof list.subject === "string" ? list.subject : "";
  const since = typeof list.since === "string" ? list.since : "";
  const until = typeof list.until === "string" ? list.until : "";
  const limit = typeof list.limit === "number" && list.limit > 0 ? list.limit : PAGE_SIZE;
  const before = typeof list.before === "number" && list.before > 0 ? list.before : 0;

  const filtersActive = Boolean(actor || kind || subject || since || until);

  // `before` is part of the key so each cursor is its own cached page; the filters are part of it so
  // a narrowed read never serves a page cached under a different question.
  const page = useQuery({
    queryKey: ["audit", { actor, kind, subject, since, until, limit, before }],
    queryFn: () =>
      listEvents({
        actor: actor || undefined,
        kind: kind || undefined,
        subject: subject || undefined,
        since: since ? toInstant(since, "start") : undefined,
        until: until ? toInstant(until, "end") : undefined,
        limit,
        before: before || undefined,
      }),
  });

  const rows = page.data?.events ?? [];
  const nextBefore = page.data?.next_before ?? 0;

  const setFilter = useCallback(
    (patch: Record<string, string | number>) => {
      // Any filter change restarts the walk: a cursor from a different filter's page would address
      // the wrong rows. Clearing the cursor is the whole reason this is not a plain setList.
      setList({ ...patch, before: 0 });
    },
    [setList],
  );

  const clearFilters = () => setFilter({ actor: "", kind: "", subject: "", since: "", until: "" });

  const filterSummary = useMemo(() => {
    const parts: string[] = [];
    if (actor) parts.push(`actor ${actor}`);
    if (kind) parts.push(`kind ${kind}`);
    if (subject) parts.push(`subject starts ${subject}`);
    if (since || until) parts.push(`${since || "the beginning"} to ${until || "now"}`);
    return parts.join(" · ");
  }, [actor, kind, subject, since, until]);

  const header = (
    <PageHeader
      title="Audit record"
      description="Everything the app did, recorded once. This log is append-only: no event can be edited or removed."
      crumbs={[{ label: "Board", to: "/" }, { label: "Audit record" }]}
    />
  );

  return (
    <>
      {header}

      <Toolbar>
        <div className="w-44">
          <TextInput
            label="Actor"
            placeholder="who did it"
            value={actor}
            onChange={(event) => setFilter({ actor: event.target.value })}
          />
        </div>
        <div className="w-44">
          <TextInput
            label="Kind"
            placeholder="housekeeping.remove"
            value={kind}
            onChange={(event) => setFilter({ kind: event.target.value })}
          />
        </div>
        <div className="w-44">
          <TextInput
            label="Subject"
            placeholder="fleet-console or #157"
            value={subject}
            onChange={(event) => setFilter({ subject: event.target.value })}
          />
        </div>
        <div className="w-40">
          <TextInput
            label="Since"
            type="date"
            value={since}
            onChange={(event) => setFilter({ since: event.target.value })}
          />
        </div>
        <div className="w-40">
          <TextInput
            label="Until"
            type="date"
            value={until}
            onChange={(event) => setFilter({ until: event.target.value })}
          />
        </div>
        {filtersActive ? (
          <Button variant="quiet" size="sm" onClick={clearFilters}>
            Clear filters
          </Button>
        ) : null}
      </Toolbar>

      <div className="flex min-w-0 flex-col">
        {page.isLoading ? (
          <LoadingRows rows={Math.min(limit, 8)} />
        ) : page.error ? (
          // A failed read is its own state: the store could not be read, which is not the same
          // answer as "there are no events". Rendering an empty table here would hide a broken log.
          <ErrorState
            title="The audit log could not be read"
            error={page.error}
            onRetry={() => void page.refetch()}
          />
        ) : rows.length === 0 ? (
          filtersActive ? (
            <NoMatchesState query={filterSummary} onClear={clearFilters} />
          ) : (
            <EmptyState
              title="No events recorded yet"
              body="The event log is append-only and starts empty on a fresh instance. Every action the app takes appears here, unchanged, as soon as it happens."
            />
          )
        ) : (
          <AuditTable rows={rows} />
        )}

        <div className="flex flex-wrap items-center gap-2 border-t border-rule px-3 py-1.5">
          <p className="text-[0.6875rem] text-ink-muted">
            {page.isLoading
              ? "Reading the log"
              : page.error
                ? "The log did not load"
                : rows.length === 0
                  ? "No events on this page"
                  : `${rows.length} ${rows.length === 1 ? "event" : "events"} on this page`}
          </p>
          <div className="ml-auto flex items-center gap-1">
            <IconButton
              label="Newest page"
              disabled={before === 0}
              onClick={() => setList({ before: 0 })}
              className="border border-rule-strong"
            >
              <ChevronLeft size={14} />
            </IconButton>
            <IconButton
              label="Older page"
              // An empty cursor is the last page: the server returned fewer rows than the limit and
              // there is nothing further back to ask for.
              disabled={nextBefore === 0}
              onClick={() => setList({ before: nextBefore })}
              className="border border-rule-strong"
            >
              <ChevronRight size={14} />
            </IconButton>
          </div>
        </div>
      </div>
    </>
  );
}

/** The rows, newest first. The table is hand-rolled rather than `DataGrid` because the grid pages by
 *  offset and design D1 rejects that for an append-only ledger: the server pages by keyset, so the
 *  pager is two cursor buttons and there is no total to print. */
function AuditTable({ rows }: { rows: AuditEvent[] }) {
  return (
    <div className="min-w-0 overflow-x-auto">
      <table className="w-full min-w-[46rem] border-collapse text-left">
        <caption className="sr-only">Audit events, newest first</caption>
        <thead>
          <tr className="border-b border-rule-strong">
            {COLUMNS.map((column) => (
              <th
                key={column}
                scope="col"
                className="px-3 py-1.5 text-[0.6875rem] font-semibold tracking-[0.02em] text-ink-muted uppercase"
              >
                {column}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((event) => (
            <tr key={event.id} className="border-b border-rule last:border-b-0 hover:bg-hover">
              <td className="px-3 py-[var(--density-row-pad-y)] text-body align-middle">
                <Link
                  to="/audit/$eventId"
                  params={{ eventId: String(event.id) }}
                  className="text-ink underline decoration-rule-strong hover:decoration-ink"
                  title={`${dateTime(event.at)} — open this event`}
                >
                  <time dateTime={event.at} className="num">
                    {relativeTime(event.at)}
                  </time>
                </Link>
              </td>
              <td className="px-3 py-[var(--density-row-pad-y)] text-body text-ink align-middle font-mono text-[0.6875rem]">
                {event.actor}
              </td>
              <td className="px-3 py-[var(--density-row-pad-y)] text-body text-ink align-middle font-mono">
                {event.kind}
              </td>
              <td className="px-3 py-[var(--density-row-pad-y)] text-body text-ink align-middle">{event.subject}</td>
              <td className="px-3 py-[var(--density-row-pad-y)] text-body text-ink align-middle">
                <span className="line-clamp-2" title={event.detail}>
                  {orDash(event.detail)}
                </span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** The date input gives a bare `YYYY-MM-DD`; the server's closed set wants an RFC3339 instant. A
 *  bare date is read as the whole day, so `until` must reach the day's last instant. `at` is written
 *  RFC3339Nano, so `T23:59:59Z` stops a full second short and silently hides events in the
 *  sub-second tail between .000000001 and .999999999. Nine fractional digits is the most RFC3339Nano
 *  can express, so that is the day's last instant. */
function toInstant(date: string, edge: "start" | "end"): string {
  return edge === "start" ? `${date}T00:00:00Z` : `${date}T23:59:59.999999999Z`;
}
