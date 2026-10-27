/** Roles board.
 *
 *  One ruled table holds every role: what it grants, whether the panel shipped it or an operator
 *  made it, and how many identities hold it. A role name opens the detail board where the grants
 *  are reviewed and changed. Creating a custom role is one dialog, and a duplicate name is refused
 *  with the service's own message rather than a generic failure. */

import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Plus, ShieldCheck } from "lucide-react";
import { DataGrid, GridSearch } from "../components/grid/DataGrid";
import type { GridColumn } from "../components/grid/DataGrid";
import {
  Badge,
  Button,
  Checkbox,
  Dialog,
  Panel,
  SelectInput,
  TextArea,
  TextInput,
  Toolbar,
  useToast,
} from "../components/ui";
import { Gate } from "../access/access";
import { RequirePermission } from "../components/RequirePermission";
import { PageHeader } from "../shell/PageHeader";
import { useListState } from "../lib/urlState";
import { DEFAULT_PAGE_SIZE, createRole, listRoles } from "../mock/api";
import { PERMISSIONS, PERMISSION_GROUPS, PERMISSION_IDS } from "../mock/permissions";
import type { Role } from "../mock/types";
import { plural, truncate } from "../lib/format";

type RoleRow = Role & { memberCount: number };

export function RoleListScreen() {
  const [list, setList] = useListState<{
    page: number;
    pageSize: number;
    sort: string;
    dir: "asc" | "desc";
    q: string;
    kind: string;
  }>({ page: 1, pageSize: DEFAULT_PAGE_SIZE, sort: "name", dir: "asc", q: "", kind: "all" });
  const [createOpen, setCreateOpen] = useState(false);

  const rolesQuery = useQuery({ queryKey: ["roles"], queryFn: listRoles });
  const roles = useMemo(() => rolesQuery.data ?? [], [rolesQuery.data]);

  const query = list.q.trim().toLowerCase();
  const filtered = useMemo(() => {
    return roles.filter((role) => {
      if (list.kind === "system" && !role.system) return false;
      if (list.kind === "custom" && role.system) return false;
      if (!query) return true;
      return (
        role.name.toLowerCase().includes(query) ||
        role.description.toLowerCase().includes(query) ||
        role.permissions.some((permission) => permission.toLowerCase().includes(query))
      );
    });
  }, [roles, list.kind, query]);

  const sorted = useMemo(() => {
    const factor = list.dir === "asc" ? 1 : -1;
    const rows = [...filtered];
    rows.sort((a, b) => {
      switch (list.sort) {
        case "kind":
          return ((a.system ? 0 : 1) - (b.system ? 0 : 1)) * factor;
        case "members":
          return (a.memberCount - b.memberCount) * factor;
        case "permissions":
          return (a.permissions.length - b.permissions.length) * factor;
        case "description":
          return a.description.localeCompare(b.description) * factor;
        default:
          return a.name.localeCompare(b.name) * factor;
      }
    });
    return rows;
  }, [filtered, list.sort, list.dir]);

  const pageRows = useMemo(
    () => sorted.slice((list.page - 1) * list.pageSize, list.page * list.pageSize),
    [sorted, list.page, list.pageSize],
  );

  const filtering = query !== "" || list.kind !== "all";

  const onSort = (columnId: string) => {
    if (list.sort === columnId) setList({ dir: list.dir === "asc" ? "desc" : "asc" });
    else setList({ sort: columnId, dir: "asc" });
  };

  const columns: GridColumn<RoleRow>[] = [
    {
      id: "name",
      header: "Role",
      sortable: true,
      width: "13rem",
      cell: (role) => (
        <Link
          to="/roles/$roleId"
          params={{ roleId: role.id }}
          className="font-semibold text-ink underline decoration-rule-strong underline-offset-2 hover:decoration-ink"
        >
          {role.name}
        </Link>
      ),
    },
    {
      id: "kind",
      header: "Kind",
      sortable: true,
      width: "8rem",
      cell: (role) =>
        role.system ? (
          <Badge tone="ink">
            <ShieldCheck size={11} aria-hidden="true" className="mr-1" />
            System
          </Badge>
        ) : (
          <Badge tone="quiet">Custom</Badge>
        ),
    },
    {
      id: "description",
      header: "Description",
      sortable: true,
      cell: (role) => (
        <span className="text-ink-muted" title={role.description}>
          {role.description ? truncate(role.description, 96) : "—"}
        </span>
      ),
    },
    {
      id: "members",
      header: "Members",
      sortable: true,
      align: "right",
      width: "6.5rem",
      cell: (role) => <span className="num">{role.memberCount.toLocaleString("en-GB")}</span>,
    },
    {
      id: "permissions",
      header: "Permissions",
      sortable: true,
      align: "right",
      width: "7.5rem",
      cell: (role) => (
        <span className="num">
          {role.permissions.length}
          <span className="text-ink-muted"> / {PERMISSION_IDS.length}</span>
        </span>
      ),
    },
  ];

  const newRoleButton = (
    <Button variant="primary" size="sm" icon={<Plus size={14} />} onClick={() => setCreateOpen(true)}>
      New role
    </Button>
  );

  return (
    <RequirePermission permission="roles.read" what="the roles board" title="Roles">
      <PageHeader
        title="Roles"
        count={
          filtering ? `${sorted.length} of ${roles.length} roles` : plural(roles.length, "role")
        }
        description="What each role grants, and how many identities hold it. System roles ship with the panel; custom roles are made here."
        crumbs={[{ label: "Board", to: "/" }, { label: "Roles" }]}
        actions={<Gate permission="roles.write">{newRoleButton}</Gate>}
      />

      <div className="flex flex-col gap-4 px-3 py-4 sm:px-5">
        <Panel>
          <Toolbar>
            <GridSearch
              value={list.q}
              onChange={(next) => setList({ q: next })}
              placeholder="Search by name, description or permission"
              label="Search roles"
            />
            <div className="w-44">
              <SelectInput
                label="Kind"
                value={list.kind}
                onChange={(event) => setList({ kind: event.target.value })}
                options={[
                  { value: "all", label: "All roles" },
                  { value: "system", label: "System-defined" },
                  { value: "custom", label: "Custom" },
                ]}
              />
            </div>
          </Toolbar>

          <DataGrid<RoleRow>
            rows={pageRows}
            columns={columns}
            rowKey={(role) => role.id}
            caption="Roles on this board, with kind, member count and permission count"
            total={sorted.length}
            page={list.page}
            pageSize={list.pageSize}
            sort={list.sort}
            dir={list.dir}
            onSort={onSort}
            onPage={(page) => setList({ page })}
            onPageSize={(pageSize) => setList({ pageSize })}
            isLoading={rolesQuery.isLoading}
            error={rolesQuery.error}
            onRetry={() => void rolesQuery.refetch()}
            emptyTitle="No roles on the board"
            emptyBody="A role is a named set of permissions. Create the first custom role and its grants will appear here; the four system roles normally ship with the panel."
            emptyAction={<Gate permission="roles.write">{newRoleButton}</Gate>}
            noMatchesQuery={filtering ? query || "the current filters" : undefined}
            onClearFilters={() => setList({ q: "", kind: "all" })}
          />
        </Panel>
      </div>

      <CreateRoleDialog open={createOpen} onClose={() => setCreateOpen(false)} />
    </RequirePermission>
  );
}

/** The create form. It never pre-checks for a duplicate: the service owns that rule, so its own
 *  message ("A role called … already exists.") is what the operator reads. */
function CreateRoleDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [granted, setGranted] = useState<string[]>([]);
  const [nameError, setNameError] = useState<string | null>(null);

  const create = useMutation({ mutationFn: createRole });

  const close = () => {
    setName("");
    setDescription("");
    setGranted([]);
    setNameError(null);
    onClose();
  };

  const toggle = (id: string, on: boolean) => {
    setGranted((current) => (on ? [...current, id] : current.filter((permission) => permission !== id)));
  };

  const submit = async () => {
    setNameError(null);
    try {
      const role = await create.mutateAsync({ name, description, permissions: granted });
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["roles"] }),
        queryClient.invalidateQueries({ queryKey: ["audit"] }),
        queryClient.invalidateQueries({ queryKey: ["dashboard"] }),
      ]);
      toast.done(
        `Role “${role.name}” created`,
        `${plural(granted.length, "permission")} granted. No member holds it yet.`,
      );
      close();
    } catch (error) {
      const message = error instanceof Error ? error.message : "The role was not created.";
      setNameError(message);
      toast.problem("Role not created", message);
    }
  };

  return (
    <Dialog
      open={open}
      onClose={close}
      title="New role"
      description="A custom role: a name, what it is for, and the permissions it starts with."
      width="lg"
      footer={
        <>
          <Button variant="quiet" onClick={close}>
            Cancel
          </Button>
          <Button variant="primary" busy={create.isPending} onClick={() => void submit()}>
            Create role
          </Button>
        </>
      }
    >
      <form
        className="flex flex-col gap-4"
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <TextInput
          label="Role name"
          value={name}
          onChange={(event) => setName(event.target.value)}
          error={nameError ?? undefined}
          hint="Shown wherever this role grants access. Names must be unique."
          autoFocus
        />
        <TextArea
          label="Description"
          value={description}
          rows={2}
          onChange={(event) => setDescription(event.target.value)}
          hint="One or two sentences on what this role is for."
        />

        <fieldset className="flex flex-col gap-3">
          <legend className="label text-ink-muted">Initial grants</legend>
          <p className="text-[0.6875rem] text-ink-muted">
            Pick what this role starts with. Grants can be changed on the role's own board after it is created.
          </p>
          {PERMISSION_GROUPS.map((group) => (
            <div key={group} className="flex flex-col gap-2">
              <p className="label text-ink-muted">{group}</p>
              <div className="grid gap-x-6 gap-y-2 sm:grid-cols-2">
                {PERMISSIONS.filter((permission) => permission.group === group).map((permission) => (
                  <Checkbox
                    key={permission.id}
                    label={permission.label}
                    hint={permission.destructive ? `${permission.id} · confirmed before it runs` : permission.id}
                    checked={granted.includes(permission.id)}
                    onChange={(on) => toggle(permission.id, on)}
                  />
                ))}
              </div>
            </div>
          ))}
        </fieldset>
      </form>
    </Dialog>
  );
}
