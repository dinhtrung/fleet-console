/** The user directory.
 *
 *  One board for finding an account, narrowing the board with search and filters, reading a row
 *  without reading every cell, and changing several accounts at once. The grid owns sort, paging,
 *  selection and the four list states; this screen owns the filter row, the bulk confirmation and
 *  the receipt. Status is the magnet, in a fixed slot on every row. */

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { Ban, RotateCcw, UserPlus, UserX } from "lucide-react";
import {
  bulkSetUserStatus,
  createUser,
  DEFAULT_PAGE_SIZE,
  listOrganizations,
  listRoles,
  listUsers,
} from "../mock/api";
import { USER_STATUSES } from "../mock/types";
import type { ListQuery, User, UserStatus } from "../mock/types";
import { useAuth } from "../auth/session";
import { useScope } from "../shell/scope";
import { Gate } from "../access/access";
import { RequirePermission } from "../components/RequirePermission";
import { PageHeader } from "../shell/PageHeader";
import { useListState } from "../lib/urlState";
import { DataGrid, GridSearch } from "../components/grid/DataGrid";
import type { GridColumn } from "../components/grid/DataGrid";
import {
  Badge,
  Button,
  ConfirmDialog,
  Dialog,
  Panel,
  SelectInput,
  StatusMagnet,
  TextInput,
  Toolbar,
  useToast,
} from "../components/ui";
import { absoluteDate, dateTime, initials, plural, relativeTime } from "../lib/format";

const ORG_QUERY = { page: 1, pageSize: 50, sort: "name", dir: "asc" } as const;

const STATUS_LABEL: Record<UserStatus, string> = {
  active: "Active",
  invited: "Invited",
  suspended: "Suspended",
  deactivated: "Deactivated",
};

/** A stable empty list so the roles column does not rebuild while the roles query is in flight. */
const NO_ROLES: { id: string; name: string }[] = [];

function bulkVerb(status: UserStatus): string {
  if (status === "suspended") return "suspended";
  if (status === "deactivated") return "deactivated";
  return "reactivated";
}

function bulkCopy(status: UserStatus, count: number) {
  const noun = plural(count, "user");
  if (status === "suspended") {
    return {
      title: `Suspend ${noun}?`,
      description: `${noun} selected will not be able to sign in until reactivated. Existing sessions are not ended. Every change is recorded in the audit record.`,
      confirmLabel: "Suspend selected",
    };
  }
  if (status === "deactivated") {
    return {
      title: `Deactivate ${noun}?`,
      description: `${noun} selected will not be able to sign in and their active sessions will be ended. Reactivating restores access. Every change is recorded in the audit record.`,
      confirmLabel: "Deactivate selected",
    };
  }
  return {
    title: `Reactivate ${noun}?`,
    description: `${noun} selected will be able to sign in again. Every change is recorded in the audit record.`,
    confirmLabel: "Reactivate selected",
  };
}

export function UserListScreen() {
  const { identity } = useAuth();
  const scope = useScope();
  const navigate = useNavigate();
  const toast = useToast();
  const queryClient = useQueryClient();

  const [state, setState] = useListState({
    page: 1,
    pageSize: DEFAULT_PAGE_SIZE,
    sort: "name",
    dir: "asc",
    q: "",
    status: "",
    role: "",
    org: scope.orgId ?? "",
  });

  const [selected, setSelected] = useState<string[]>([]);
  const [bulk, setBulk] = useState<UserStatus | null>(null);
  const [bulkError, setBulkError] = useState<string | null>(null);

  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({ name: "", email: "", roleId: "" });
  const [formError, setFormError] = useState<{ name?: string; email?: string }>({});

  const dir: "asc" | "desc" = state.dir === "desc" ? "desc" : "asc";
  const query: ListQuery = {
    page: state.page,
    pageSize: state.pageSize,
    sort: state.sort,
    dir,
    q: state.q.trim() || undefined,
    status: state.status || undefined,
    roleId: state.role || undefined,
    orgId: state.org || undefined,
  };

  const usersQuery = useQuery({ queryKey: ["users", query], queryFn: () => listUsers(query) });
  const rolesQuery = useQuery({ queryKey: ["roles"], queryFn: listRoles });
  const orgsQuery = useQuery({
    queryKey: ["organizations", ORG_QUERY],
    queryFn: () => listOrganizations(ORG_QUERY),
  });

  const roles = rolesQuery.data ?? NO_ROLES;
  const orgs = orgsQuery.data?.items ?? [];
  const rows = usersQuery.data?.items ?? [];
  const total = usersQuery.data?.total ?? 0;
  const page = usersQuery.data?.page ?? state.page;

  const roleName = (id: string) => roles.find((role) => role.id === id)?.name ?? id;

  const filtersActive = Boolean(state.q.trim() || state.status || state.role || state.org);
  // A filtered board with no rows is "nothing matches", never "the directory is empty".
  const noMatchesQuery = filtersActive ? state.q.trim() || "the current filters" : undefined;

  const columns: GridColumn<User>[] = [
    {
      id: "name",
      header: "Person",
      sortable: true,
      hideable: false,
      width: "20rem",
      cell: (row) => (
        <div className="flex min-w-0 items-center gap-2">
          <span
            aria-hidden="true"
            className="flex size-6 shrink-0 items-center justify-center rounded-chip bg-plum font-mono text-[0.625rem] font-semibold text-on-plum"
          >
            {initials(row.name)}
          </span>
          <span className="min-w-0">
            <Link
              to="/users/$userId"
              params={{ userId: row.id }}
              className="block truncate text-body font-semibold text-ink hover:underline"
              title={row.name}
            >
              {row.name}
            </Link>
            <span className="block truncate font-mono text-[0.625rem] text-ink-muted" title={row.email}>
              {row.email}
            </span>
          </span>
        </div>
      ),
    },
    {
      id: "status",
      header: "Status",
      sortable: true,
      width: "7rem",
      cell: (row) => <StatusMagnet status={row.status} />,
    },
    {
      id: "roles",
      header: "Roles",
      sortable: false,
      cell: (row) => (
        <span className="flex flex-wrap items-center gap-1">
          {row.roleIds.slice(0, 2).map((id) => (
            <Badge key={id} tone="quiet">
              {roleName(id)}
            </Badge>
          ))}
          {row.roleIds.length > 2 ? <Badge tone="tint">+{row.roleIds.length - 2}</Badge> : null}
          {row.roleIds.length === 0 ? <span className="text-ink-muted">No role</span> : null}
        </span>
      ),
    },
    {
      id: "lastActiveAt",
      header: "Last active",
      sortable: true,
      align: "right",
      width: "8rem",
      cell: (row) => (
        <span className="num" title={row.lastActiveAt ? dateTime(row.lastActiveAt) : "No recorded activity"}>
          {row.lastActiveAt ? relativeTime(row.lastActiveAt) : "Never"}
        </span>
      ),
    },
    {
      id: "createdAt",
      header: "Created",
      sortable: true,
      align: "right",
      width: "8rem",
      cell: (row) => (
        <span className="num" title={dateTime(row.createdAt)}>
          {absoluteDate(row.createdAt)}
        </span>
      ),
    },
  ];

  const onSort = (columnId: string) => {
    setState({
      sort: columnId,
      dir: state.sort === columnId && state.dir === "asc" ? "desc" : "asc",
      page: 1,
    });
  };

  const clearFilters = () => {
    setState({ q: "", status: "", role: "", org: scope.orgId ?? "", page: 1 });
  };

  const refreshWrites = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["users"] }),
      queryClient.invalidateQueries({ queryKey: ["dashboard"] }),
      queryClient.invalidateQueries({ queryKey: ["audit"] }),
    ]);
  };

  const bulkMutation = useMutation({
    mutationFn: (input: { ids: string[]; status: UserStatus }) => bulkSetUserStatus(input.ids, input.status),
  });

  const createMutation = useMutation({
    mutationFn: (input: { name: string; email: string; roleId: string }) =>
      createUser({
        name: input.name,
        email: input.email,
        roleIds: input.roleId ? [input.roleId] : [],
        orgIds: [],
      }),
  });

  const requestBulk = (status: UserStatus) => {
    setBulkError(null);
    setBulk(status);
  };

  const applyBulk = async () => {
    if (!bulk) return;
    const refusal = bulk === "suspended" || bulk === "deactivated";
    const selfId = identity?.userId;
    const selfExcluded = Boolean(refusal && selfId && selected.includes(selfId));
    // The operator's own account is never suspended or deactivated, in bulk or on its own record.
    const targets = selected.filter((id) => !(refusal && id === selfId));

    if (targets.length === 0) {
      setBulk(null);
      setSelected([]);
      toast.problem(
        "Nothing changed",
        "An operator cannot suspend or deactivate their own account. Select other users to change.",
      );
      return;
    }

    setBulkError(null);
    try {
      const result = await bulkMutation.mutateAsync({ ids: targets, status: bulk });
      await refreshWrites();
      setSelected([]);
      setBulk(null);
      const detail: string[] = [];
      if (result.skipped > 0) detail.push(`${plural(result.skipped, "user")} already ${bulk}`);
      if (selfExcluded) detail.push("your own account was left unchanged");
      const note = detail.join(" · ");
      if (result.changed > 0) {
        toast.done(`${plural(result.changed, "user")} ${bulkVerb(bulk)}`, note || undefined);
      } else {
        toast.problem(`No users ${bulkVerb(bulk)}`, note || "Every selected user was already in that state.");
      }
    } catch (e) {
      setBulkError(e instanceof Error ? e.message : "The bulk change was refused.");
    }
  };

  const submitCreate = async () => {
    setFormError({});
    try {
      await createMutation.mutateAsync({ name: form.name, email: form.email, roleId: form.roleId });
      await refreshWrites();
      toast.done(
        "User invited",
        `${form.name.trim()} was created in the invited state and now appears in the directory.`,
      );
      setCreating(false);
      setForm({ name: "", email: "", roleId: "" });
      setState({ page: 1 });
    } catch (e) {
      const message = e instanceof Error ? e.message : "The user could not be created.";
      if (message.toLowerCase().includes("email") || message.includes("@")) setFormError({ email: message });
      else setFormError({ name: message });
    }
  };

  const bulkDialog = bulk ? bulkCopy(bulk, selected.length) : null;

  return (
    <RequirePermission permission="users.read" what="The user directory" title="Users">
      <PageHeader
        title="Users"
        count={plural(total, "user")}
        crumbs={[{ label: "Board", to: "/" }, { label: "Users" }]}
        actions={
          <Gate permission="users.invite">
            <Button variant="primary" icon={<UserPlus size={13} />} onClick={() => setCreating(true)}>
              Invite user
            </Button>
          </Gate>
        }
      />

      <div className="min-w-0 px-3 py-3 sm:px-5">
        <Panel>
          <Toolbar>
            <GridSearch
              value={state.q}
              onChange={(q) => setState({ q })}
              placeholder="Search by name or email"
            />
            <div className="w-40">
              <SelectInput
                label="Status"
                value={state.status}
                onChange={(e) => setState({ status: e.target.value })}
                options={[
                  { value: "", label: "All statuses" },
                  ...USER_STATUSES.map((status) => ({ value: status, label: STATUS_LABEL[status] })),
                ]}
              />
            </div>
            <div className="w-44">
              <SelectInput
                label="Role"
                value={state.role}
                onChange={(e) => setState({ role: e.target.value })}
                options={[
                  { value: "", label: "All roles" },
                  ...roles.map((role) => ({ value: role.id, label: role.name })),
                ]}
              />
            </div>
            <div className="w-44">
              <SelectInput
                label="Organization"
                value={state.org}
                onChange={(e) => setState({ org: e.target.value })}
                options={[
                  { value: "", label: "All organizations" },
                  ...orgs.map((org) => ({ value: org.id, label: org.name })),
                ]}
              />
            </div>
            {filtersActive ? (
              <Button variant="quiet" size="sm" onClick={clearFilters}>
                Clear filters
              </Button>
            ) : null}
          </Toolbar>

          <DataGrid
            rows={rows}
            columns={columns}
            rowKey={(row) => row.id}
            caption="User directory"
            total={total}
            page={page}
            pageSize={state.pageSize}
            sort={state.sort}
            dir={dir}
            onSort={onSort}
            onPage={(next) => setState({ page: next })}
            onPageSize={(size) => setState({ pageSize: size })}
            onRowClick={(row) => void navigate({ to: "/users/$userId", params: { userId: row.id } })}
            selectable
            selected={selected}
            onSelectedChange={setSelected}
            bulkBar={(_ids, clear) => (
              <Gate permission="users.deactivate">
                <Button
                  variant="outline"
                  size="sm"
                  icon={<Ban size={12} />}
                  onClick={() => requestBulk("suspended")}
                >
                  Suspend
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  icon={<UserX size={12} />}
                  onClick={() => requestBulk("deactivated")}
                >
                  Deactivate
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  icon={<RotateCcw size={12} />}
                  onClick={() => requestBulk("active")}
                >
                  Reactivate
                </Button>
                <Button variant="quiet" size="sm" onClick={clear}>
                  Clear selection
                </Button>
              </Gate>
            )}
            isLoading={usersQuery.isLoading}
            error={usersQuery.error}
            onRetry={() => void usersQuery.refetch()}
            emptyTitle="No users on this board yet"
            emptyBody="The directory is empty. Invite the first person and they will appear here in the invited state, ready to be given a role."
            emptyAction={
              <Gate permission="users.invite">
                <Button variant="outline" size="sm" icon={<UserPlus size={13} />} onClick={() => setCreating(true)}>
                  Invite user
                </Button>
              </Gate>
            }
            noMatchesQuery={noMatchesQuery}
            onClearFilters={clearFilters}
            sweepKey={scope.sweepKey}
          />
        </Panel>
      </div>

      {bulkDialog ? (
        <ConfirmDialog
          open
          onClose={() => {
            if (!bulkMutation.isPending) {
              setBulk(null);
              setBulkError(null);
            }
          }}
          onConfirm={() => void applyBulk()}
          title={bulkDialog.title}
          description={bulkDialog.description}
          confirmLabel={bulkDialog.confirmLabel}
          busy={bulkMutation.isPending}
          error={bulkError ?? undefined}
        />
      ) : null}

      <Dialog
        open={creating}
        onClose={() => {
          if (!createMutation.isPending) {
            setCreating(false);
            setFormError({});
          }
        }}
        title="Invite a user"
        description="The account is created in the invited state and appears in the directory immediately. The person sets their own password the first time they sign in."
        footer={
          <>
            <Button
              variant="quiet"
              onClick={() => {
                setCreating(false);
                setFormError({});
              }}
              disabled={createMutation.isPending}
            >
              Cancel
            </Button>
            <Button variant="primary" onClick={() => void submitCreate()} busy={createMutation.isPending}>
              Invite user
            </Button>
          </>
        }
      >
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            void submitCreate();
          }}
        >
          <TextInput
            label="Display name"
            value={form.name}
            onChange={(e) => setForm((current) => ({ ...current, name: e.target.value }))}
            error={formError.name}
            autoComplete="off"
          />
          <TextInput
            label="Work email"
            type="email"
            value={form.email}
            onChange={(e) => setForm((current) => ({ ...current, email: e.target.value }))}
            error={formError.email}
            autoComplete="off"
          />
          <SelectInput
            label="Role"
            value={form.roleId}
            onChange={(e) => setForm((current) => ({ ...current, roleId: e.target.value }))}
            options={[
              { value: "", label: "No role yet" },
              ...roles.map((role) => ({ value: role.id, label: role.name })),
            ]}
            hint="The role can be changed later from the user's record."
          />
        </form>
      </Dialog>
    </RequirePermission>
  );
}
