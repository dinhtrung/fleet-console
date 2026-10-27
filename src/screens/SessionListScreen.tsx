/** The workspace session inventory, scoped to the current organization.
 *
 *  This is the security board: every active session on the board, who holds it, where it came from
 *  and when it was last seen. The operator's own session is marked, because revoking it does not
 *  leave a dead board behind — it ends the operator's session and returns them to sign-in. */

import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { LogOut, MonitorSmartphone, X } from "lucide-react";
import { DataGrid, GridSearch } from "../components/grid/DataGrid";
import type { GridColumn } from "../components/grid/DataGrid";
import { Badge, Button, ConfirmDialog, IconButton, Toolbar, useToast } from "../components/ui";
import { RequirePermission } from "../components/RequirePermission";
import { Gate } from "../access/access";
import { PageHeader } from "../shell/PageHeader";
import { useScope } from "../shell/scope";
import { useAuth } from "../auth/session";
import { useListState } from "../lib/urlState";
import { DEFAULT_PAGE_SIZE, listSessions, revokeOtherSessions, revokeSession } from "../mock/api";
import type { ListQuery } from "../mock/types";
import { dateTime, number, plural, relativeTime } from "../lib/format";

type SessionRow = Awaited<ReturnType<typeof listSessions>>["items"][number];

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "Something went wrong. Nothing was changed.";
}

export function SessionListScreen() {
  return (
    <RequirePermission permission="sessions.read" what="The session board" title="Sessions">
      <SessionBoard />
    </RequirePermission>
  );
}

function SessionBoard() {
  const { orgId, sweepKey } = useScope();
  const { signOut } = useAuth();
  const toast = useToast();
  const queryClient = useQueryClient();

  const [list, setList] = useListState({
    page: 1,
    pageSize: DEFAULT_PAGE_SIZE,
    sort: "startedAt",
    dir: "desc",
    q: "",
  });

  const q = typeof list.q === "string" ? list.q : "";
  const sort = typeof list.sort === "string" ? list.sort : "startedAt";
  const dir: "asc" | "desc" = list.dir === "asc" ? "asc" : "desc";

  const params: ListQuery = {
    page: list.page,
    pageSize: list.pageSize,
    sort,
    dir,
    q: q || undefined,
    orgId: orgId ?? undefined,
  };

  const sessions = useQuery({ queryKey: ["sessions", params], queryFn: () => listSessions(params) });
  // The bulk action revokes every session on the board except the current one, not just the scoped
  // ones, so the number it will state has to come from an unscoped count.
  const board = useQuery({ queryKey: ["sessions", "board-count"], queryFn: () => listSessions({ pageSize: 1 }) });

  const rows = sessions.data?.items ?? [];
  const total = sessions.data?.total ?? 0;
  const otherCount = Math.max(0, (board.data?.total ?? 0) - 1);

  const [pending, setPending] = useState<SessionRow | null>(null);
  const [bulkOpen, setBulkOpen] = useState(false);

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ["sessions"] });
    void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
    void queryClient.invalidateQueries({ queryKey: ["audit"] });
  };

  const revokeOne = useMutation({
    mutationFn: (row: SessionRow) => revokeSession(row.id),
    onSuccess: (_result, row) => {
      setPending(null);
      refresh();
      if (row.current) {
        toast.done("Your session was revoked", "Sign in again to keep working on this board.");
        signOut("revoked");
      } else {
        toast.done("Session revoked", `${row.device} · ${row.location} must sign in again.`);
      }
    },
  });

  const revokeAll = useMutation({
    mutationFn: () => revokeOtherSessions(),
    onSuccess: (result) => {
      setBulkOpen(false);
      refresh();
      toast.done(`${plural(result.revoked, "session")} revoked`, "Every session except this one was signed out.");
    },
  });

  const onSort = (columnId: string) => {
    setList(sort === columnId ? { dir: dir === "asc" ? "desc" : "asc" } : { sort: columnId, dir: "asc" });
  };

  const columns: GridColumn<SessionRow>[] = [
    {
      id: "user",
      header: "Account",
      sortable: true,
      hideable: false,
      width: "15rem",
      cell: (row) => (
        <div className="flex min-w-0 flex-col">
          <Link
            to="/users/$userId"
            params={{ userId: row.userId }}
            className="truncate font-semibold text-ink hover:underline"
          >
            {row.user?.name ?? row.userId}
          </Link>
          <span className="truncate font-mono text-[0.6875rem] text-ink-muted" title={row.user?.email ?? row.userId}>
            {row.user?.email ?? row.userId}
          </span>
        </div>
      ),
    },
    {
      id: "current",
      header: "Session",
      hideable: false,
      width: "8.5rem",
      cell: (row) =>
        row.current ? (
          <span title="The session you are signed in with">
            <Badge tone="ink">
              <MonitorSmartphone size={11} aria-hidden="true" /> This session
            </Badge>
          </span>
        ) : null,
    },
    {
      id: "device",
      header: "Device",
      sortable: true,
      width: "13rem",
      cell: (row) => (
        <div className="flex min-w-0 flex-col">
          <span className="truncate text-ink" title={row.device}>
            {row.device}
          </span>
          <span className="truncate text-[0.6875rem] text-ink-muted">{row.browser}</span>
        </div>
      ),
    },
    {
      id: "location",
      header: "Location",
      sortable: true,
      cell: (row) => (
        <span className="truncate" title={row.location}>
          {row.location}
        </span>
      ),
    },
    {
      id: "ip",
      header: "IP address",
      width: "9rem",
      cell: (row) => (
        <span className="font-mono text-[0.6875rem] text-ink" title={row.ip}>
          {row.ip}
        </span>
      ),
    },
    {
      id: "startedAt",
      header: "Started",
      sortable: true,
      width: "8rem",
      cell: (row) => (
        <time dateTime={row.startedAt} title={dateTime(row.startedAt)} className="num text-ink">
          {relativeTime(row.startedAt)}
        </time>
      ),
    },
    {
      id: "lastSeenAt",
      header: "Last seen",
      sortable: true,
      width: "8rem",
      cell: (row) => (
        <time dateTime={row.lastSeenAt} title={dateTime(row.lastSeenAt)} className="num text-ink">
          {relativeTime(row.lastSeenAt)}
        </time>
      ),
    },
    {
      id: "actions",
      header: "Actions",
      align: "right",
      hideable: false,
      width: "5.5rem",
      cell: (row) => (
        <Gate permission="sessions.revoke">
          <IconButton label={`Revoke the session on ${row.device}`} onClick={() => setPending(row)}>
            <LogOut size={14} aria-hidden="true" />
          </IconButton>
        </Gate>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        title="Sessions"
        count={`${number(total)} ${total === 1 ? "active session" : "active sessions"}`}
        description={
          orgId
            ? "Active sessions belonging to the current organization. Revoking one ends it for that device only."
            : "Every active session on the board. Revoking one ends it for that device only."
        }
        crumbs={[{ label: "Board", to: "/" }, { label: "Sessions" }]}
        actions={
          <Gate permission="sessions.revoke">
            {!board.isSuccess ? (
              <Button variant="outline" busy>
                Checking sessions
              </Button>
            ) : otherCount > 0 ? (
              <Button
                variant="outline"
                icon={<LogOut size={13} aria-hidden="true" />}
                onClick={() => setBulkOpen(true)}
              >
                Revoke other sessions
              </Button>
            ) : (
              <Button variant="outline" disabled title="This is the only active session on the board">
                No other sessions
              </Button>
            )}
          </Gate>
        }
      />

      <Toolbar>
        <GridSearch
          value={q}
          onChange={(value) => setList({ q: value })}
          placeholder="Search account, device, IP or location"
        />
        {q ? (
          <Button variant="quiet" size="sm" icon={<X size={12} aria-hidden="true" />} onClick={() => setList({ q: "" })}>
            Clear search
          </Button>
        ) : null}
        <Gate permission="sessions.revoke">
          {board.isSuccess && otherCount === 0 ? (
            <p className="text-[0.6875rem] text-ink-muted">
              This is the only active session — there is nothing else to revoke.
            </p>
          ) : null}
        </Gate>
      </Toolbar>

      <DataGrid
        rows={rows}
        columns={columns}
        rowKey={(row) => row.id}
        caption="Active sessions, scoped to the current organization"
        total={total}
        page={sessions.data?.page ?? list.page}
        pageSize={sessions.data?.pageSize ?? list.pageSize}
        sort={sort}
        dir={dir}
        onSort={onSort}
        onPage={(page) => setList({ page })}
        onPageSize={(pageSize) => setList({ pageSize })}
        isLoading={sessions.isLoading}
        error={sessions.error}
        onRetry={() => void sessions.refetch()}
        emptyTitle="No active sessions"
        emptyBody={
          orgId
            ? "No session on this board belongs to the current organization. Widen the scope to the whole board to see every session."
            : "No sessions are active on this board right now. One appears here as soon as someone signs in."
        }
        noMatchesQuery={q || undefined}
        onClearFilters={() => setList({ q: "" })}
        sweepKey={sweepKey}
      />

      <ConfirmDialog
        open={pending !== null}
        onClose={() => {
          setPending(null);
          revokeOne.reset();
        }}
        onConfirm={() => {
          if (pending) revokeOne.mutate(pending);
        }}
        title={pending?.current ? "Revoke your own session?" : "Revoke this session?"}
        description={
          pending
            ? pending.current
              ? "This is the session you are using. Revoking it signs you out immediately and you will have to sign in again."
              : `${pending.user?.name ?? pending.userId}'s session on ${pending.device} (${pending.location}) will be signed out. That device must sign in again.`
            : ""
        }
        confirmLabel={pending?.current ? "Revoke and sign out" : "Revoke session"}
        busy={revokeOne.isPending}
        error={revokeOne.isError ? errorMessage(revokeOne.error) : undefined}
      />

      <ConfirmDialog
        open={bulkOpen}
        onClose={() => {
          setBulkOpen(false);
          revokeAll.reset();
        }}
        onConfirm={() => revokeAll.mutate()}
        title={`Revoke ${plural(otherCount, "other session")}?`}
        description={`This signs out ${plural(otherCount, "session")} on every device except the one you are using. Each of those devices must sign in again.`}
        confirmLabel={`Revoke ${plural(otherCount, "session")}`}
        busy={revokeAll.isPending}
        error={revokeAll.isError ? errorMessage(revokeAll.error) : undefined}
      />
    </>
  );
}
