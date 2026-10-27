/** One role's board.
 *
 *  It answers three questions in order: who holds this role, what would a change to it do, and what
 *  exactly does it grant. The permission matrix is grouped by area and staged — ticking a box never
 *  writes; the blast radius updates while the change is pending, and only Save calls the service. A
 *  system role states that it is protected from deletion; a custom role is deleted only after a
 *  typed confirmation, and the service's refusal (members still hold it, or it is the last role able
 *  to administer the workspace) is surfaced verbatim. */

import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { Lock, Save, Trash, TriangleAlert, Undo2 } from "lucide-react";
import {
  Badge,
  Button,
  Checkbox,
  ConfirmDialog,
  EmptyState,
  ErrorState,
  LoadingRows,
  NotFoundState,
  Panel,
  PanelHead,
  StatusMagnet,
  useToast,
} from "../components/ui";
import { Gate, useAccess } from "../access/access";
import { RequirePermission } from "../components/RequirePermission";
import { PageHeader } from "../shell/PageHeader";
import { deleteRole, getRole, updateRole } from "../mock/api";
import { PERMISSIONS, PERMISSION_GROUPS, isAdministrationRole } from "../mock/permissions";
import { isApiError } from "../mock/types";
import { plural } from "../lib/format";

export function RoleDetailScreen({ roleId }: { roleId: string }) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const { can } = useAccess();
  const canWrite = can("roles.write");

  const roleQuery = useQuery({ queryKey: ["role", roleId], queryFn: () => getRole(roleId) });
  const role = roleQuery.data?.role;
  const members = roleQuery.data?.members ?? [];

  const [staged, setStaged] = useState<string[]>([]);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const syncedRoleId = useRef<string | null>(null);

  const save = useMutation({ mutationFn: (permissions: string[]) => updateRole(roleId, { permissions }) });
  const remove = useMutation({ mutationFn: () => deleteRole(roleId) });

  // Seed the staged set once per role. A later refetch (window focus) must not wipe unsaved edits.
  useEffect(() => {
    if (role && syncedRoleId.current !== role.id) {
      syncedRoleId.current = role.id;
      setStaged([...role.permissions]);
    }
  }, [role]);

  const savedPermissions = role?.permissions ?? [];
  const added = staged.filter((permission) => !savedPermissions.includes(permission));
  const removed = savedPermissions.filter((permission) => !staged.includes(permission));
  const dirty = [...savedPermissions].sort().join(",") !== [...staged].sort().join(",");
  const dropsAdministration = role !== undefined && isAdministrationRole(savedPermissions) && !isAdministrationRole(staged);

  const toggle = (id: string, on: boolean) => {
    setSaveError(null);
    setStaged((current) => (on ? [...current, id] : current.filter((permission) => permission !== id)));
  };

  const discard = () => {
    setSaveError(null);
    setStaged([...savedPermissions]);
  };

  const submitSave = async () => {
    setSaveError(null);
    try {
      const updated = await save.mutateAsync([...staged].sort());
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["role", roleId] }),
        queryClient.invalidateQueries({ queryKey: ["roles"] }),
        queryClient.invalidateQueries({ queryKey: ["audit"] }),
        queryClient.invalidateQueries({ queryKey: ["dashboard"] }),
      ]);
      setStaged([...updated.permissions]);
      toast.done(
        `“${updated.name}” updated`,
        `${plural(members.length, "identity", "identities")} hold this role and were affected.`,
      );
    } catch (error) {
      const message = error instanceof Error ? error.message : "The role was not updated.";
      setSaveError(message);
      toast.problem("Role not updated", message);
    }
  };

  const confirmDelete = async () => {
    if (!role) return;
    // Defence in depth: even reached directly, a system role is refused and left intact.
    if (role.system) {
      setDeleteError("This is a system-defined role, which is protected and cannot be deleted.");
      toast.problem("System role protected", `${role.name} is a system role and cannot be deleted.`);
      return;
    }
    setDeleteError(null);
    try {
      await remove.mutateAsync();
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["roles"] }),
        queryClient.invalidateQueries({ queryKey: ["audit"] }),
        queryClient.invalidateQueries({ queryKey: ["dashboard"] }),
      ]);
      toast.done(`“${role.name}” deleted`, "Its grants no longer apply to anyone.");
      await navigate({ to: "/roles" });
    } catch (error) {
      const message = error instanceof Error ? error.message : "The role was not deleted.";
      setDeleteError(message);
      toast.problem("Role not deleted", message);
    }
  };

  if (roleQuery.isLoading) {
    return (
      <RequirePermission permission="roles.read" what="this role" title="Role">
        <PageHeader title="Role" crumbs={[{ label: "Board", to: "/" }, { label: "Roles", to: "/roles" }, { label: "Loading" }]} />
        <div className="px-3 py-4 sm:px-5">
          <Panel>
            <LoadingRows rows={6} />
          </Panel>
        </div>
      </RequirePermission>
    );
  }

  if (roleQuery.error || !role) {
    const notFound = isApiError(roleQuery.error) && roleQuery.error.code === "not_found";
    return (
      <RequirePermission permission="roles.read" what="this role" title="Role">
        <PageHeader
          title={notFound ? "No such role" : "Role"}
          crumbs={[{ label: "Board", to: "/" }, { label: "Roles", to: "/roles" }, { label: notFound ? "Not found" : "Error" }]}
        />
        <div className="px-3 py-4 sm:px-5">
          {notFound ? (
            <NotFoundState what="role">
              <Button variant="outline" size="sm" onClick={() => void navigate({ to: "/roles" })}>
                Back to roles
              </Button>
            </NotFoundState>
          ) : (
            <Panel>
              <ErrorState error={roleQuery.error} onRetry={() => void roleQuery.refetch()} />
            </Panel>
          )}
        </div>
      </RequirePermission>
    );
  }

  return (
    <RequirePermission permission="roles.read" what="this role" title="Role">
      <PageHeader
        title={role.name}
        count={plural(members.length, "member")}
        description={role.description || undefined}
        crumbs={[{ label: "Board", to: "/" }, { label: "Roles", to: "/roles" }, { label: role.name }]}
        actions={
          <>
            {role.system ? <Badge tone="ink">System role</Badge> : <Badge tone="quiet">Custom role</Badge>}
            {!role.system ? (
              <Gate permission="roles.write">
                <Button variant="outline" size="sm" icon={<Trash size={14} />} onClick={() => setDeleteOpen(true)}>
                  Delete role
                </Button>
              </Gate>
            ) : null}
          </>
        }
      />

      <div className="flex flex-col gap-4 px-3 py-4 sm:px-5">
        {role.system ? (
          <p className="flex items-start gap-2 border border-rule bg-panel px-3 py-2 text-body text-ink-muted">
            <Lock size={14} aria-hidden="true" className="mt-0.5 shrink-0 text-ink" />
            <span>
              <span className="font-semibold text-ink">{role.name}</span> is a system-defined role. It is protected from
              deletion; its grants can still be edited.
            </span>
          </p>
        ) : null}

        <Panel>
          <PanelHead
            title="Members"
            count={plural(members.length, "member")}
            description="Identities holding this role. Open one to see the account."
          />
          {members.length === 0 ? (
            <EmptyState
              title="Nobody holds this role"
              body="No account on the board has this role, so a change to its grants cannot affect anyone yet. Assign it from a user's board."
            />
          ) : (
            <ul className="divide-y divide-[var(--rule)]">
              {members.map((member) => (
                <li key={member.id}>
                  <Link
                    to="/users/$userId"
                    params={{ userId: member.id }}
                    className="flex items-center gap-3 px-3 py-2 hover:bg-hover"
                  >
                    <span className="min-w-0">
                      <span className="block truncate text-body font-semibold text-ink">{member.name}</span>
                      <span className="block truncate font-mono text-[0.625rem] text-ink-muted">{member.email}</span>
                    </span>
                    <span className="ml-auto flex items-center gap-2">
                      <StatusMagnet status={member.status} />
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </Panel>

        <Panel>
          <PanelHead
            title="Blast radius"
            count={dirty ? plural(added.length + removed.length, "staged change") : "no staged changes"}
            description="What saving the staged grants would do, before anything is written."
          />
          <div className="flex flex-col gap-2 px-3 py-3">
            {dirty ? (
              <>
                <p className="text-body text-ink">
                  Saving affects{" "}
                  <span className="num font-semibold">{members.length.toLocaleString("en-GB")}</span>{" "}
                  {members.length === 1 ? "identity" : "identities"} holding <span className="font-semibold">{role.name}</span>.
                </p>
                <dl className="grid gap-1">
                  <div className="flex flex-wrap items-baseline gap-x-2">
                    <dt className="label text-ink-muted">Adding</dt>
                    <dd className="font-mono text-body text-ink">{added.length > 0 ? added.join(", ") : "—"}</dd>
                  </div>
                  <div className="flex flex-wrap items-baseline gap-x-2">
                    <dt className="label text-ink-muted">Removing</dt>
                    <dd className="font-mono text-body text-ink">{removed.length > 0 ? removed.join(", ") : "—"}</dd>
                  </div>
                </dl>
                {dropsAdministration ? (
                  <p className="flex items-start gap-2 border border-rule-strong px-2 py-1.5 text-[0.6875rem] text-ink-muted">
                    <TriangleAlert size={13} aria-hidden="true" className="mt-0.5 shrink-0 text-attention" />
                    <span>
                      This removes the administration grant. The panel will refuse the save if {role.name} is the only
                      role that can administer the workspace.
                    </span>
                  </p>
                ) : null}
              </>
            ) : (
              <p className="text-body text-ink-muted">
                Nothing is staged. The grants below match what is saved, and{" "}
                <span className="num">{members.length.toLocaleString("en-GB")}</span>{" "}
                {members.length === 1 ? "identity holds" : "identities hold"} this role.
              </p>
            )}
          </div>
        </Panel>

        <Panel>
          <PanelHead
            title="Permission grants"
            description="Grouped by area of the admin surface. Changes are staged here and written only when you save."
            actions={
              canWrite ? (
                <>
                  <Button
                    variant="outline"
                    size="sm"
                    icon={<Undo2 size={14} />}
                    disabled={!dirty || save.isPending}
                    onClick={discard}
                  >
                    Discard
                  </Button>
                  <Button
                    variant="primary"
                    size="sm"
                    icon={<Save size={14} />}
                    busy={save.isPending}
                    disabled={!dirty}
                    onClick={() => void submitSave()}
                  >
                    Save grants
                  </Button>
                </>
              ) : null
            }
          />
          <div className="flex flex-col gap-4 px-3 py-3">
            {!canWrite ? (
              <p className="text-[0.6875rem] text-ink-muted">
                These grants are read-only for your identity. Changing them needs the{" "}
                <span className="font-mono text-ink">roles.write</span> permission.
              </p>
            ) : null}

            {PERMISSION_GROUPS.map((group) => (
              <fieldset key={group} className="flex flex-col gap-2">
                <legend className="label text-ink-muted">{group}</legend>
                <div className="grid gap-x-6 gap-y-2 sm:grid-cols-2 lg:grid-cols-3">
                  {PERMISSIONS.filter((permission) => permission.group === group).map((permission) => (
                    <Checkbox
                      key={permission.id}
                      label={permission.label}
                      hint={permission.destructive ? `${permission.id} · confirmed before it runs` : permission.id}
                      checked={staged.includes(permission.id)}
                      disabled={!canWrite}
                      onChange={(on) => toggle(permission.id, on)}
                    />
                  ))}
                </div>
              </fieldset>
            ))}

            {saveError ? (
              <p role="alert" className="border border-rule-strong px-2 py-1.5 text-body font-semibold text-attention">
                {saveError}
              </p>
            ) : null}
          </div>
        </Panel>
      </div>

      <ConfirmDialog
        open={deleteOpen}
        onClose={() => {
          setDeleteOpen(false);
          setDeleteError(null);
        }}
        onConfirm={() => void confirmDelete()}
        title={`Delete ${role.name}?`}
        description={`This removes the role and its ${plural(role.permissions.length, "grant")} for good. It is refused if members still hold it or if it is the last role able to administer the workspace.`}
        confirmLabel="Delete role"
        requireTyped={role.name}
        typedLabel={`Type “${role.name}” to confirm`}
        busy={remove.isPending}
        error={deleteError ?? undefined}
      />
    </RequirePermission>
  );
}
