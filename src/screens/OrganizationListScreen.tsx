/** The organization directory.
 *
 *  Every organization on the board, shown with the attributes an operator scans for — plan, status,
 *  membership and age — through the shared grid, so sorting, search, pagination and the four list
 *  states behave exactly as they do on every other list. A row opens that organization's record.
 *  The directory is board-wide: it is the source of the scope selector, so it is not itself filtered
 *  by the current scope. */

import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { DataGrid, GridSearch } from "../components/grid/DataGrid";
import type { GridColumn } from "../components/grid/DataGrid";
import { Badge, OrgMark, Panel, StatusMagnet, Toolbar } from "../components/ui";
import { RequirePermission } from "../components/RequirePermission";
import { PageHeader } from "../shell/PageHeader";
import { useScope } from "../shell/scope";
import { useListState } from "../lib/urlState";
import { absoluteDate, number } from "../lib/format";
import { listOrganizations } from "../mock/api";
import type { Organization } from "../mock/types";

type OrgRow = Organization & { memberCount: number };

export function OrganizationListScreen() {
  return (
    <RequirePermission permission="orgs.read" what="The organization directory" title="Organizations">
      <OrganizationDirectory />
    </RequirePermission>
  );
}

function OrganizationDirectory() {
  const navigate = useNavigate();
  const { sweepKey } = useScope();
  const [params, setParams] = useListState({
    page: 1,
    pageSize: 25,
    sort: "name",
    dir: "asc",
    q: "",
  });

  const sort = typeof params.sort === "string" ? params.sort : "name";
  const dir = params.dir === "desc" ? "desc" : "asc";
  const search = typeof params.q === "string" ? params.q : "";

  const query = useQuery({
    queryKey: ["organizations", params],
    queryFn: () =>
      listOrganizations({
        page: Number(params.page),
        pageSize: Number(params.pageSize),
        sort,
        dir,
        q: search,
      }),
  });

  const rows = query.data?.items ?? [];
  const total = query.data?.total ?? 0;

  const columns: GridColumn<OrgRow>[] = [
    {
      id: "name",
      header: "Organization",
      sortable: true,
      cell: (row) => (
        <span className="flex items-center gap-2">
          <OrgMark slug={row.slug} name={row.name} />
          <Link
            to="/organizations/$orgId"
            params={{ orgId: row.id }}
            onClick={(event) => event.stopPropagation()}
            className="font-semibold text-ink"
          >
            {row.name}
          </Link>
        </span>
      ),
    },
    {
      id: "slug",
      header: "Slug",
      width: "12rem",
      cell: (row) => (
        <span className="font-mono text-[0.6875rem] text-ink-muted" title={row.slug}>
          {row.slug}
        </span>
      ),
    },
    {
      id: "plan",
      header: "Plan",
      sortable: true,
      width: "7.5rem",
      cell: (row) => <Badge className="capitalize">{row.plan}</Badge>,
    },
    {
      id: "status",
      header: "Status",
      sortable: true,
      width: "7.5rem",
      cell: (row) => <StatusMagnet status={row.status} />,
    },
    {
      id: "memberCount",
      header: "Members",
      sortable: true,
      align: "right",
      width: "6rem",
      cell: (row) => <span className="num">{number(row.memberCount)}</span>,
    },
    {
      id: "createdAt",
      header: "Created",
      sortable: true,
      width: "9rem",
      cell: (row) => (
        <span className="num" title={row.createdAt}>
          {absoluteDate(row.createdAt)}
        </span>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        title="Organizations"
        count={query.isSuccess ? `${number(total)} ${total === 1 ? "organization" : "organizations"}` : undefined}
        crumbs={[{ label: "Board", to: "/" }, { label: "Organizations" }]}
        description="Every organization on the board, with its plan, status and membership. Open one to read its record and manage its members."
      />
      <div className="px-3 py-3 sm:px-5">
        <Panel>
          <Toolbar>
            <GridSearch
              value={search}
              onChange={(next) => setParams({ q: next })}
              placeholder="Search by name or slug"
            />
          </Toolbar>
          <DataGrid
            rows={rows}
            columns={columns}
            rowKey={(row) => row.id}
            caption="Organizations"
            total={total}
            page={Number(params.page)}
            pageSize={Number(params.pageSize)}
            sort={sort}
            dir={dir}
            onSort={(columnId) =>
              setParams({
                sort: columnId,
                dir: sort === columnId && dir === "asc" ? "desc" : "asc",
              })
            }
            onPage={(page) => setParams({ page })}
            onPageSize={(pageSize) => setParams({ pageSize })}
            onRowClick={(row) => void navigate({ to: "/organizations/$orgId", params: { orgId: row.id } })}
            isLoading={query.isLoading}
            error={query.error}
            onRetry={() => void query.refetch()}
            sweepKey={sweepKey}
            emptyTitle="No organizations on the board yet"
            emptyBody="An organization groups the people who share a workspace. When one exists it appears here with its plan, status and member count. The board is genuinely empty — there is no filter to clear."
            noMatchesQuery={search === "" ? undefined : search}
            onClearFilters={() => setParams({ q: "" })}
          />
        </Panel>
      </div>
    </>
  );
}
