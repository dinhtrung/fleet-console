/** The audit record: append-only, and nothing here can be changed or removed.
 *
 *  Every state-changing action is one row — actor, action, target, IP and time — filterable by
 *  free text, action, actor and date range. A filter that matches nothing says so; it is never
 *  confused with a board that has no events at all. */

import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { ArrowUpRight, X } from "lucide-react";
import { DataGrid, GridSearch } from "../components/grid/DataGrid";
import type { GridColumn } from "../components/grid/DataGrid";
import { Button, SelectInput, TextInput, Toolbar } from "../components/ui";
import { RequirePermission } from "../components/RequirePermission";
import { PageHeader } from "../shell/PageHeader";
import { useScope } from "../shell/scope";
import { useListState } from "../lib/urlState";
import { auditActions, DEFAULT_PAGE_SIZE, listAudit, listUsers } from "../mock/api";
import type { AuditEvent, ListQuery, User } from "../mock/types";
import { dateTime, number, relativeTime } from "../lib/format";

/** The API pages at 50 at most, so the actor picker walks the pages until the board is exhausted. */
async function fetchActors(orgId: string | undefined): Promise<User[]> {
  const pageSize = 50;
  const first = await listUsers({ pageSize, page: 1, orgId });
  const all = [...first.items];
  const pages = Math.min(Math.ceil(first.total / pageSize), 4);
  for (let page = 2; page <= pages; page += 1) {
    const next = await listUsers({ pageSize, page, orgId });
    all.push(...next.items);
  }
  return all.sort((a, b) => a.name.localeCompare(b.name));
}

/** A target names a user, an organization or a role: link to it where such a surface exists. */
function TargetLink({ event }: { event: AuditEvent }) {
  const className = "text-ink underline decoration-rule-strong hover:decoration-ink";
  switch (event.targetType) {
    case "user":
      return (
        <Link to="/users/$userId" params={{ userId: event.targetId }} className={className}>
          {event.targetLabel}
        </Link>
      );
    case "organization":
      return (
        <Link to="/organizations/$orgId" params={{ orgId: event.targetId }} className={className}>
          {event.targetLabel}
        </Link>
      );
    case "role":
      return (
        <Link to="/roles/$roleId" params={{ roleId: event.targetId }} className={className}>
          {event.targetLabel}
        </Link>
      );
    case "settings":
      return (
        <Link to="/settings" className={className}>
          {event.targetLabel}
        </Link>
      );
    default:
      return <span className="text-ink">{event.targetLabel}</span>;
  }
}

export function AuditListScreen() {
  return (
    <RequirePermission permission="audit.read" what="The audit record" title="Audit record">
      <AuditBoard />
    </RequirePermission>
  );
}

function AuditBoard() {
  const { orgId, sweepKey } = useScope();

  const [list, setList] = useListState({
    page: 1,
    pageSize: DEFAULT_PAGE_SIZE,
    sort: "at",
    dir: "desc",
    q: "",
    action: "",
    actorId: "",
    from: "",
    to: "",
  });

  const q = typeof list.q === "string" ? list.q : "";
  const sort = typeof list.sort === "string" ? list.sort : "at";
  const dir: "asc" | "desc" = list.dir === "asc" ? "asc" : "desc";
  const action = typeof list.action === "string" ? list.action : "";
  const actorId = typeof list.actorId === "string" ? list.actorId : "";
  const from = typeof list.from === "string" ? list.from : "";
  const to = typeof list.to === "string" ? list.to : "";

  const params: ListQuery = {
    page: list.page,
    pageSize: list.pageSize,
    sort,
    dir,
    q: q || undefined,
    action: action || undefined,
    actorId: actorId || undefined,
    from: from || undefined,
    to: to || undefined,
    orgId: orgId || undefined,
  };

  const events = useQuery({ queryKey: ["audit", params], queryFn: () => listAudit(params) });
  const actions = useQuery({ queryKey: ["audit", "actions"], queryFn: () => auditActions() });
  const actors = useQuery({
    queryKey: ["users", { scope: orgId ?? "all", picker: "actors" }],
    queryFn: () => fetchActors(orgId || undefined),
  });

  const rows = events.data?.items ?? [];
  const total = events.data?.total ?? 0;
  const filtersActive = Boolean(q || action || actorId || from || to);
  const actorName = actors.data?.find((user) => user.id === actorId)?.name;

  const filterParts: string[] = [];
  if (q) filterParts.push(`“${q}”`);
  if (action) filterParts.push(`action ${action}`);
  if (actorId) filterParts.push(`actor ${actorName ?? actorId}`);
  if (from || to) filterParts.push(`${from || "the beginning"} to ${to || "now"}`);
  const filterSummary = filterParts.join(" · ");

  const clearFilters = () => setList({ q: "", action: "", actorId: "", from: "", to: "" });

  const onSort = (columnId: string) => {
    setList(sort === columnId ? { dir: dir === "asc" ? "desc" : "asc" } : { sort: columnId, dir: "asc" });
  };

  const columns: GridColumn<AuditEvent>[] = [
    {
      id: "at",
      header: "When",
      sortable: true,
      hideable: false,
      width: "10rem",
      cell: (event) => (
        <Link
          to="/audit/$eventId"
          params={{ eventId: event.id }}
          className="inline-flex items-center gap-1 text-ink hover:underline"
          title={`${dateTime(event.at)} — open this event`}
        >
          <time dateTime={event.at} className="num">
            {relativeTime(event.at)}
          </time>
          <ArrowUpRight size={11} aria-hidden="true" className="text-ink-muted" />
        </Link>
      ),
    },
    {
      id: "actor",
      header: "Actor",
      width: "14rem",
      cell: (event) => (
        <div className="flex min-w-0 flex-col">
          <span className="truncate text-ink" title={event.actorName}>
            {event.actorName}
          </span>
          <span className="truncate font-mono text-[0.6875rem] text-ink-muted" title={event.actorId}>
            {event.actorId}
          </span>
        </div>
      ),
    },
    {
      id: "action",
      header: "Action",
      width: "15rem",
      cell: (event) => <span className="font-mono text-ink">{event.action}</span>,
    },
    {
      id: "target",
      header: "Target",
      cell: (event) => <TargetLink event={event} />,
    },
    {
      id: "ip",
      header: "IP address",
      width: "9rem",
      cell: (event) => (
        <span className="font-mono text-[0.6875rem] text-ink" title={event.ip}>
          {event.ip}
        </span>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        title="Audit record"
        count={`${number(total)} ${total === 1 ? "event" : "events"}`}
        description="Every state-changing action, recorded once. This record is append-only: no event can be edited or removed."
        crumbs={[{ label: "Board", to: "/" }, { label: "Audit record" }]}
      />

      <Toolbar>
        <GridSearch
          value={q}
          onChange={(value) => setList({ q: value })}
          placeholder="Search actor, action or target"
        />
        <div className="w-52">
          <SelectInput
            label="Action"
            value={action}
            onChange={(event) => setList({ action: event.target.value })}
            options={[
              { value: "", label: "Any action" },
              ...(actions.data ?? []).map((value) => ({ value, label: value })),
            ]}
          />
        </div>
        <div className="w-56">
          <SelectInput
            label="Actor"
            value={actorId}
            onChange={(event) => setList({ actorId: event.target.value })}
            options={[
              { value: "", label: "Any actor" },
              ...(actors.data ?? []).map((user) => ({ value: user.id, label: user.name })),
            ]}
          />
        </div>
        <div className="w-40">
          <TextInput label="From" type="date" value={from} onChange={(event) => setList({ from: event.target.value })} />
        </div>
        <div className="w-40">
          <TextInput label="To" type="date" value={to} onChange={(event) => setList({ to: event.target.value })} />
        </div>
        {filtersActive ? (
          <Button variant="quiet" size="sm" icon={<X size={12} aria-hidden="true" />} onClick={clearFilters}>
            Clear filters
          </Button>
        ) : null}
      </Toolbar>

      <DataGrid
        rows={rows}
        columns={columns}
        rowKey={(event) => event.id}
        caption="Audit events, newest first"
        total={total}
        page={events.data?.page ?? list.page}
        pageSize={events.data?.pageSize ?? list.pageSize}
        sort={sort}
        dir={dir}
        onSort={onSort}
        onPage={(page) => setList({ page })}
        onPageSize={(pageSize) => setList({ pageSize })}
        isLoading={events.isLoading}
        error={events.error}
        onRetry={() => void events.refetch()}
        emptyTitle="No events recorded yet"
        emptyBody="The audit record is append-only and starts empty on a fresh board. Every state-changing action appears here, unchanged, as soon as it happens."
        noMatchesQuery={filtersActive ? filterSummary || "these filters" : undefined}
        onClearFilters={clearFilters}
        sweepKey={sweepKey}
      />
    </>
  );
}
