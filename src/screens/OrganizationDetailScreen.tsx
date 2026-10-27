/** One organization's record and its membership.
 *
 *  Facts at the top, the roster below, and the three membership writes (add, change role, remove)
 *  behind dialogs: removal and any change to ownership ask first, and a refusal from the service —
 *  the last owner may not be removed or demoted — is printed on the control that attempted it rather
 *  than swallowed. This organization's own scope control sets whose board the rest of the panel
 *  reads. An organization with no members is a real state, not an error: the roster says so and the
 *  member count stays zero. */

import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Crosshair, MoreHorizontal, UserPlus } from "lucide-react";
import { DataGrid } from "../components/grid/DataGrid";
import type { GridColumn } from "../components/grid/DataGrid";
import {
  Badge,
  Button,
  ConfirmDialog,
  Dialog,
  ErrorState,
  FieldRow,
  LoadingRows,
  Menu,
  NotFoundState,
  OrgMark,
  Panel,
  PanelHead,
  SelectInput,
  StatusMagnet,
  TextInput,
} from "../components/ui";
import { RequirePermission } from "../components/RequirePermission";
import { Gate, useAccess } from "../access/access";
import { PageHeader } from "../shell/PageHeader";
import { useScope } from "../shell/scope";
import { useToast } from "../components/ui/Toast";
import { cn } from "../lib/cn";
import { absoluteDate, number } from "../lib/format";
import {
  addOrganizationMember,
  DEFAULT_PAGE_SIZE,
  getOrganization,
  listUsers,
  removeOrganizationMember,
  updateOrganizationMember,
} from "../mock/api";
import { isApiError } from "../mock/types";
import type { MembershipRole, User } from "../mock/types";

type OrgMember = Awaited<ReturnType<typeof getOrganization>>["members"][number];

const ROLE_LABEL: Record<MembershipRole, string> = {
  owner: "Owner",
  admin: "Admin",
  member: "Member",
};

const ROLE_OPTIONS: { value: string; label: string }[] = [
  { value: "owner", label: "Owner" },
  { value: "admin", label: "Admin" },
  { value: "member", label: "Member" },
];

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : "Something went wrong.";
}

function memberCompare(a: OrgMember, b: OrgMember, field: string): number {
  if (field === "role") return a.memberRole.localeCompare(b.memberRole);
  if (field === "joined") return a.joinedAt.localeCompare(b.joinedAt);
  return a.user.name.localeCompare(b.user.name);
}

export function OrganizationDetailScreen({ orgId }: { orgId: string }) {
  return (
    <RequirePermission permission="orgs.read" what="An organization's record" title="Organization">
      <OrganizationDetail orgId={orgId} />
    </RequirePermission>
  );
}

function OrganizationDetail({ orgId }: { orgId: string }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const { orgId: scopedOrgId, setOrgId } = useScope();
  const { can } = useAccess();

  const query = useQuery({ queryKey: ["organization", orgId], queryFn: () => getOrganization(orgId) });

  const members = query.data?.members ?? [];
  const orgName = query.data?.organization.name ?? "this organization";

  const [addOpen, setAddOpen] = useState(false);
  const [userQuery, setUserQuery] = useState("");
  const [selectedUser, setSelectedUser] = useState<User | null>(null);
  const [newRole, setNewRole] = useState<MembershipRole>("member");

  const [roleTarget, setRoleTarget] = useState<OrgMember | null>(null);
  const [roleDraft, setRoleDraft] = useState<MembershipRole>("member");
  const [confirmRole, setConfirmRole] = useState<{ member: OrgMember; to: MembershipRole } | null>(null);

  const [removeTarget, setRemoveTarget] = useState<OrgMember | null>(null);

  const [memberSort, setMemberSort] = useState("name");
  const [memberDir, setMemberDir] = useState<"asc" | "desc">("asc");
  const [memberPage, setMemberPage] = useState(1);
  const [memberPageSize, setMemberPageSize] = useState<number>(DEFAULT_PAGE_SIZE);

  const directory = useQuery({
    queryKey: ["users", { q: userQuery, pageSize: 25 }],
    queryFn: () => listUsers({ q: userQuery, pageSize: 25 }),
    enabled: addOpen,
  });

  const memberIds = new Set(members.map((member) => member.userId));
  const candidates = (directory.data?.items ?? []).filter((user) => !memberIds.has(user.id));

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ["organization", orgId] });
    void queryClient.invalidateQueries({ queryKey: ["organizations"] });
    void queryClient.invalidateQueries({ queryKey: ["users"] });
    void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
    void queryClient.invalidateQueries({ queryKey: ["audit"] });
  };

  const addMember = useMutation({
    mutationFn: (input: { userId: string; role: MembershipRole }) =>
      addOrganizationMember(orgId, input.userId, input.role),
    onSuccess: (_membership, input) => {
      invalidate();
      toast.done("Member added", `${ROLE_LABEL[input.role]} in ${orgName}.`);
      setAddOpen(false);
      setSelectedUser(null);
      setUserQuery("");
      setNewRole("member");
    },
    onError: (error) => toast.problem("The member was not added", messageOf(error)),
  });

  const changeRole = useMutation({
    mutationFn: (input: { member: OrgMember; role: MembershipRole }) =>
      updateOrganizationMember(orgId, input.member.userId, input.role),
    onSuccess: (_result, input) => {
      invalidate();
      toast.done(
        "Organization role changed",
        `${input.member.user.name} is now ${ROLE_LABEL[input.role]} of ${orgName}.`,
      );
      setRoleTarget(null);
      setConfirmRole(null);
    },
    onError: (error) => toast.problem("The role was not changed", messageOf(error)),
  });

  const removeMember = useMutation({
    mutationFn: (userId: string) => removeOrganizationMember(orgId, userId),
    onSuccess: (_result, userId) => {
      const name = members.find((member) => member.userId === userId)?.user.name ?? "The member";
      invalidate();
      toast.done("Member removed", `${name} no longer belongs to ${orgName}.`);
      setRemoveTarget(null);
    },
    onError: (error) => toast.problem("The member was not removed", messageOf(error)),
  });

  const openAdd = () => {
    addMember.reset();
    setSelectedUser(null);
    setUserQuery("");
    setNewRole("member");
    setAddOpen(true);
  };

  const sortedMembers = useMemo(() => {
    const ordered = [...members];
    ordered.sort((a, b) => {
      const comparison = memberCompare(a, b, memberSort);
      return memberDir === "asc" ? comparison : -comparison;
    });
    return ordered;
  }, [members, memberSort, memberDir]);

  const memberTotal = sortedMembers.length;
  const maxPage = Math.max(1, Math.ceil(memberTotal / memberPageSize));
  const safePage = Math.min(memberPage, maxPage);
  const memberRows = sortedMembers.slice((safePage - 1) * memberPageSize, safePage * memberPageSize);

  const notFound = isApiError(query.error) && query.error.code === "not_found";

  if (query.isLoading) {
    return (
      <>
        <PageHeader
          title="Organization"
          crumbs={[
            { label: "Board", to: "/" },
            { label: "Organizations", to: "/organizations" },
            { label: "Loading" },
          ]}
        />
        <div className="px-3 py-3 sm:px-5">
          <Panel>
            <LoadingRows rows={5} />
          </Panel>
        </div>
      </>
    );
  }

  if (query.isError || !query.data) {
    return (
      <>
        <PageHeader
          title={notFound ? "No such organization" : "Organization"}
          crumbs={[
            { label: "Board", to: "/" },
            { label: "Organizations", to: "/organizations" },
            { label: "Unavailable" },
          ]}
        />
        <div className="px-3 py-3 sm:px-5">
          <Panel>
            {notFound ? (
              <NotFoundState what="organization">
                <Link to="/organizations" className="text-[0.6875rem] font-semibold text-ink underline">
                  Back to the organization directory
                </Link>
              </NotFoundState>
            ) : (
              <ErrorState error={query.error} onRetry={() => void query.refetch()} />
            )}
          </Panel>
        </div>
      </>
    );
  }

  const organization = query.data.organization;
  const memberCount = members.length;
  const scopedHere = scopedOrgId === orgId;

  const memberColumns: GridColumn<OrgMember>[] = [
    {
      id: "name",
      header: "Member",
      sortable: true,
      cell: (member) => (
        <span className="flex flex-col">
          <span className="font-semibold text-ink">{member.user.name}</span>
          <span className="font-mono text-[0.625rem] text-ink-muted" title={member.user.email}>
            {member.user.email}
          </span>
        </span>
      ),
    },
    {
      id: "role",
      header: "Organization role",
      sortable: true,
      width: "11rem",
      cell: (member) => <StatusMagnet status={member.memberRole} />,
    },
    {
      id: "joined",
      header: "Joined",
      sortable: true,
      width: "9rem",
      cell: (member) => (
        <span className="num" title={member.joinedAt}>
          {absoluteDate(member.joinedAt)}
        </span>
      ),
    },
  ];

  const memberActions: GridColumn<OrgMember> = {
    id: "actions",
    header: "Actions",
    align: "right",
    width: "4rem",
    hideable: false,
    cell: (member) => (
      <div className="flex justify-end">
        <Menu
          label={`Actions for ${member.user.name}`}
          trigger={<MoreHorizontal size={14} />}
          items={[
            {
              id: "role",
              label: "Change organization role",
              hint: ROLE_LABEL[member.memberRole],
              onSelect: () => {
                changeRole.reset();
                setRoleTarget(member);
                setRoleDraft(member.memberRole);
              },
            },
            {
              id: "remove",
              label: "Remove member",
              destructive: true,
              onSelect: () => {
                removeMember.reset();
                setRemoveTarget(member);
              },
            },
          ]}
        />
      </div>
    ),
  };
  if (can("orgs.write")) memberColumns.push(memberActions);

  return (
    <>
      <PageHeader
        title={organization.name}
        mark={<OrgMark slug={organization.slug} name={organization.name} size={28} />}
        count={`${number(memberCount)} ${memberCount === 1 ? "member" : "members"}`}
        crumbs={[
          { label: "Board", to: "/" },
          { label: "Organizations", to: "/organizations" },
          { label: organization.name },
        ]}
        description="The organization's plan, status and membership. Every member change is recorded in the audit record."
        actions={
          <Gate permission="orgs.write">
            <Button variant="primary" icon={<UserPlus size={14} />} onClick={openAdd}>
              Add member
            </Button>
          </Gate>
        }
      />

      <div className="flex flex-col gap-3 px-3 py-3 sm:px-5">
        <Panel>
          <PanelHead title="Organization facts" />
          <div className="grid grid-cols-2 gap-x-4 px-3 py-2 sm:grid-cols-5">
            <FieldRow label="Plan">
              <Badge className="capitalize">{organization.plan}</Badge>
            </FieldRow>
            <FieldRow label="Status">
              <StatusMagnet status={organization.status} />
            </FieldRow>
            <FieldRow label="Members">
              <span className="num">{number(memberCount)}</span>
            </FieldRow>
            <FieldRow label="Created">
              <span className="num" title={organization.createdAt}>
                {absoluteDate(organization.createdAt)}
              </span>
            </FieldRow>
            <FieldRow label="Slug">
              <span className="font-mono text-[0.6875rem]">{organization.slug}</span>
            </FieldRow>
          </div>
        </Panel>

        <Panel>
          <PanelHead title="Board scope" />
          <div className="flex flex-col gap-2 px-3 py-2">
            <p className="max-w-[70ch] text-body text-ink-muted">
              Scope is board-wide, not per screen: the overview, users, sessions and audit record all read{" "}
              {scopedHere ? "this organization alone" : "the whole board"} until it is changed, and the choice
              survives a reload.
            </p>
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-body text-ink">
                {scopedHere
                  ? `${organization.name} is the current scope of the board.`
                  : `The board is unscoped — it is not filtered to ${organization.name}.`}
              </span>
              {scopedHere ? (
                <Button variant="outline" size="sm" onClick={() => setOrgId(null)}>
                  Use the whole board
                </Button>
              ) : (
                <Button variant="outline" size="sm" icon={<Crosshair size={13} />} onClick={() => setOrgId(orgId)}>
                  Scope the board to this organization
                </Button>
              )}
            </div>
            <p role="status" aria-live="polite" className="sr-only">
              {scopedHere ? `${organization.name} is the current organization scope` : "No organization scope is set"}
            </p>
          </div>
        </Panel>

        <Panel>
          <PanelHead
            title="Members"
            count={`${number(memberCount)} ${memberCount === 1 ? "member" : "members"}`}
            description="Organization roles are held per organization; the member's board role is separate."
          />
          <DataGrid
            rows={memberRows}
            columns={memberColumns}
            rowKey={(member) => member.userId}
            caption={`Members of ${organization.name}`}
            total={memberTotal}
            page={safePage}
            pageSize={memberPageSize}
            sort={memberSort}
            dir={memberDir}
            onSort={(columnId) => {
              if (columnId === memberSort) setMemberDir((current) => (current === "asc" ? "desc" : "asc"));
              else {
                setMemberSort(columnId);
                setMemberDir("asc");
              }
              setMemberPage(1);
            }}
            onPage={(page) => setMemberPage(page)}
            onPageSize={(pageSize) => {
              setMemberPageSize(pageSize);
              setMemberPage(1);
            }}
            emptyTitle="No members yet"
            emptyBody={`${organization.name} has no members, so its member count is zero. Add someone from the user directory to give them access.`}
            emptyAction={
              <Gate permission="orgs.write">
                <Button variant="primary" size="sm" icon={<UserPlus size={13} />} onClick={openAdd}>
                  Add member
                </Button>
              </Gate>
            }
          />
        </Panel>
      </div>

      <Dialog
        open={addOpen}
        onClose={() => setAddOpen(false)}
        title="Add a member"
        description={`Choose someone from the user directory and the role they will hold in ${orgName}.`}
        footer={
          <>
            <Button variant="quiet" onClick={() => setAddOpen(false)}>
              Cancel
            </Button>
            <Button
              variant="primary"
              disabled={!selectedUser}
              busy={addMember.isPending}
              onClick={() => {
                if (selectedUser) addMember.mutate({ userId: selectedUser.id, role: newRole });
              }}
            >
              Add member
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-3">
          <TextInput
            label="Find a user"
            type="search"
            value={userQuery}
            onChange={(event) => setUserQuery(event.target.value)}
            placeholder="Search the directory by name or email"
            hint="The directory is synthetic; type to narrow it."
          />
          <div className="flex flex-col gap-1">
            <span className="label text-ink-muted">User directory</span>
            {directory.isLoading ? (
              <LoadingRows rows={4} />
            ) : directory.isError ? (
              <p className="border border-rule px-2 py-3 text-body text-attention">
                {messageOf(directory.error)}
              </p>
            ) : candidates.length === 0 ? (
              <p className="border border-rule px-2 py-3 text-body text-ink-muted">
                {userQuery
                  ? `No user matches “${userQuery}” who is not already a member.`
                  : "Every user on the board already belongs to this organization."}
              </p>
            ) : (
              <ul className="max-h-56 overflow-y-auto border border-rule">
                {candidates.map((user) => {
                  const chosen = selectedUser?.id === user.id;
                  return (
                    <li key={user.id} className="border-b border-rule last:border-b-0">
                      <button
                        type="button"
                        aria-pressed={chosen}
                        onClick={() => setSelectedUser(user)}
                        className={cn(
                          "flex w-full flex-col items-start gap-0.5 px-2 py-1.5 text-left",
                          chosen ? "bg-selected" : "hover:bg-hover",
                        )}
                      >
                        <span className="text-body font-semibold text-ink">{user.name}</span>
                        <span className="font-mono text-[0.625rem] text-ink-muted">{user.email}</span>
                      </button>
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
          <SelectInput
            label="Organization role"
            value={newRole}
            onChange={(event) => setNewRole(event.target.value as MembershipRole)}
            options={ROLE_OPTIONS}
            hint="An organization can hold more than one owner, but never fewer than one."
          />
          {addMember.isError ? (
            <p className="text-[0.6875rem] font-semibold text-attention">{messageOf(addMember.error)}</p>
          ) : null}
        </div>
      </Dialog>

      <Dialog
        open={roleTarget !== null}
        onClose={() => setRoleTarget(null)}
        title="Change organization role"
        description={roleTarget ? `Choose the role ${roleTarget.user.name} holds in ${orgName}.` : undefined}
        footer={
          <>
            <Button variant="quiet" onClick={() => setRoleTarget(null)}>
              Cancel
            </Button>
            <Button
              variant="primary"
              disabled={!roleTarget || roleDraft === roleTarget.memberRole}
              busy={changeRole.isPending && confirmRole === null}
              onClick={() => {
                if (!roleTarget || roleDraft === roleTarget.memberRole) return;
                if (roleTarget.memberRole === "owner" || roleDraft === "owner") {
                  setConfirmRole({ member: roleTarget, to: roleDraft });
                  setRoleTarget(null);
                } else {
                  changeRole.mutate({ member: roleTarget, role: roleDraft });
                }
              }}
            >
              Save role
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-3">
          <FieldRow label="Member">
            <span className="font-semibold">{roleTarget?.user.name}</span>{" "}
            <span className="font-mono text-[0.625rem] text-ink-muted">{roleTarget?.user.email}</span>
          </FieldRow>
          <SelectInput
            label="Organization role"
            value={roleDraft}
            onChange={(event) => setRoleDraft(event.target.value as MembershipRole)}
            options={ROLE_OPTIONS}
            hint="A change that transfers ownership is confirmed before it is applied."
          />
          {changeRole.isError && roleTarget !== null ? (
            <p className="text-[0.6875rem] font-semibold text-attention">{messageOf(changeRole.error)}</p>
          ) : null}
        </div>
      </Dialog>

      <ConfirmDialog
        open={confirmRole !== null}
        onClose={() => setConfirmRole(null)}
        onConfirm={() => {
          if (confirmRole) changeRole.mutate({ member: confirmRole.member, role: confirmRole.to });
        }}
        title="Change the ownership of this organization"
        description={
          confirmRole
            ? `${confirmRole.member.user.name} is currently ${ROLE_LABEL[confirmRole.member.memberRole]} of ${orgName}. Changing their role to ${ROLE_LABEL[confirmRole.to]} changes who owns this organization.`
            : ""
        }
        confirmLabel="Change role"
        busy={changeRole.isPending}
        error={changeRole.isError ? messageOf(changeRole.error) : undefined}
      />

      <ConfirmDialog
        open={removeTarget !== null}
        onClose={() => setRemoveTarget(null)}
        onConfirm={() => {
          if (removeTarget) removeMember.mutate(removeTarget.userId);
        }}
        title="Remove this member"
        description={
          removeTarget
            ? `${removeTarget.user.name} loses access to ${orgName}. Their account stays on the board and they can be added again later; the change is recorded in the audit record.`
            : ""
        }
        confirmLabel="Remove member"
        busy={removeMember.isPending}
        error={removeMember.isError ? messageOf(removeMember.error) : undefined}
      />
    </>
  );
}
