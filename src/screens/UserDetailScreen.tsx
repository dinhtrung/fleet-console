/** One user's record.
 *
 *  Who they are, what they can reach, where they belong and what they have done — with the edits
 *  and the status transitions the record allows, each confirmed and each receipted. A record that
 *  is not on the board is a not-found state, never an empty detail view. */

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Ban, RotateCcw, UserX } from "lucide-react";
import { getUser, setUserStatus, updateUser } from "../mock/api";
import { isApiError } from "../mock/types";
import type { AuditEvent, Membership, Organization, Role, User, UserStatus } from "../mock/types";
import { useAuth } from "../auth/session";
import { Gate } from "../access/access";
import { RequirePermission } from "../components/RequirePermission";
import { PageHeader } from "../shell/PageHeader";
import {
  Badge,
  Button,
  Checkbox,
  ConfirmDialog,
  ErrorState,
  FieldRow,
  LoadingRows,
  NotFoundState,
  Panel,
  PanelHead,
  StatusMagnet,
  TextInput,
  useToast,
} from "../components/ui";
import { absoluteDate, dateTime, initials, relativeTime } from "../lib/format";

function statusVerb(status: UserStatus): string {
  if (status === "suspended") return "suspended";
  if (status === "deactivated") return "deactivated";
  return "reactivated";
}

function transitionCopy(user: User, status: UserStatus) {
  if (status === "suspended") {
    return {
      title: `Suspend ${user.name}?`,
      description: `${user.name} will not be able to sign in until reactivated. Existing sessions are not ended. The change is recorded in the audit record.`,
      confirmLabel: "Suspend user",
    };
  }
  if (status === "deactivated") {
    return {
      title: `Deactivate ${user.name}?`,
      description: `${user.name} will not be able to sign in and their active sessions are ended. Reactivating restores access. The change is recorded in the audit record.`,
      confirmLabel: "Deactivate user",
    };
  }
  return {
    title: `Reactivate ${user.name}?`,
    description: `${user.name} will be able to sign in again. The change is recorded in the audit record.`,
    confirmLabel: "Reactivate user",
  };
}

export function UserDetailScreen({ userId }: { userId: string }) {
  return (
    <RequirePermission permission="users.read" what="This user's record" title="User">
      <UserDetail userId={userId} />
    </RequirePermission>
  );
}

function UserDetail({ userId }: { userId: string }) {
  const detailQuery = useQuery({ queryKey: ["user", userId], queryFn: () => getUser(userId) });
  const data = detailQuery.data;
  const user = data?.user;

  const crumbs = [
    { label: "Board", to: "/" },
    { label: "Users", to: "/users" },
    { label: user ? "User" : "Not found" },
  ];

  if (detailQuery.isLoading) {
    return (
      <>
        <PageHeader title="User" crumbs={crumbs} />
        <div className="min-w-0 px-3 py-3 sm:px-5">
          <LoadingRows rows={6} />
        </div>
      </>
    );
  }

  if (detailQuery.isError || !user) {
    const notFound = isApiError(detailQuery.error) && detailQuery.error.code === "not_found";
    return (
      <>
        <PageHeader title="User" crumbs={crumbs} />
        <div className="min-w-0 px-3 py-3 sm:px-5">
          <Panel>
            {notFound ? (
              <NotFoundState what="user">
                <Link
                  to="/users"
                  className="inline-flex min-h-7 items-center rounded-chip border border-rule-strong px-2 text-[0.6875rem] font-semibold text-ink hover:bg-hover"
                >
                  Back to the directory
                </Link>
              </NotFoundState>
            ) : (
              <ErrorState error={detailQuery.error} onRetry={() => void detailQuery.refetch()} />
            )}
          </Panel>
        </div>
      </>
    );
  }

  const roles = data?.roles ?? [];
  const memberships = data?.memberships ?? [];
  const activity = data?.activity ?? [];

  return (
    <>
      <PageHeader
        title={user.name}
        crumbs={crumbs}
        count={<StatusMagnet status={user.status} />}
        description={user.email}
      />

      <div className="mx-auto flex min-w-0 max-w-5xl flex-col gap-3 px-3 py-3 sm:px-5">
        <ProfilePanel user={user} />

        <Panel>
          <PanelHead title="Status" actions={<StatusMagnet status={user.status} />} />
          <div className="flex flex-col gap-2 px-3 py-3">
            <Gate
              permission="users.deactivate"
              fallback={
                <p className="text-[0.6875rem] text-ink-muted">
                  Suspending, deactivating and reactivating a user needs the{" "}
                  <span className="font-mono text-ink">users.deactivate</span> permission, which your roles do not grant.
                </p>
              }
            >
              <StatusActions user={user} />
            </Gate>
          </div>
        </Panel>

        <Gate
          permission="users.write"
          fallback={
            <Panel>
              <PanelHead title="Edit profile" />
              <p className="px-3 py-3 text-[0.6875rem] text-ink-muted">
                Editing a user needs the <span className="font-mono text-ink">users.write</span> permission, which your
                roles do not grant.
              </p>
            </Panel>
          }
        >
          <EditPanel user={user} />
        </Gate>

        <RolesPanel roles={roles} />
        <MembershipsPanel memberships={memberships} />
        <ActivityPanel activity={activity} />
      </div>
    </>
  );
}

function ProfilePanel({ user }: { user: User }) {
  return (
    <Panel>
      <PanelHead title="Profile" />
      <div className="flex flex-col gap-3 px-3 py-3">
        <div className="flex items-start gap-3">
          <span
            aria-hidden="true"
            className="flex size-10 shrink-0 items-center justify-center rounded-chip bg-plum font-mono text-body font-semibold text-on-plum"
          >
            {initials(user.name)}
          </span>
          <div className="min-w-0">
            <p className="break-words text-lead font-semibold text-ink">{user.name}</p>
            <p className="break-all font-mono text-body text-ink-muted">{user.email}</p>
          </div>
        </div>
        <div className="grid gap-x-6 gap-y-1 sm:grid-cols-2">
          <FieldRow label="Status">
            <StatusMagnet status={user.status} />
          </FieldRow>
          <FieldRow label="Multi-factor authentication">{user.mfa ? "Enabled" : "Disabled"}</FieldRow>
          <FieldRow label="Last active">
            <span title={user.lastActiveAt ? dateTime(user.lastActiveAt) : undefined}>
              {user.lastActiveAt ? relativeTime(user.lastActiveAt) : "No recorded activity"}
            </span>
          </FieldRow>
          <FieldRow label="Created">
            <span title={dateTime(user.createdAt)}>{absoluteDate(user.createdAt)}</span>
          </FieldRow>
          <FieldRow label="User ID">
            <span className="font-mono">{user.id}</span>
          </FieldRow>
        </div>
      </div>
    </Panel>
  );
}

function StatusActions({ user }: { user: User }) {
  const { identity } = useAuth();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [pending, setPending] = useState<UserStatus | null>(null);
  const [dialogError, setDialogError] = useState<string | null>(null);

  const mutation = useMutation({ mutationFn: (status: UserStatus) => setUserStatus(user.id, status) });

  const isSelf = identity?.userId === user.id;
  const canSuspend = user.status === "active" || user.status === "invited";
  const canDeactivate = user.status !== "deactivated";
  const canReactivate = user.status === "suspended" || user.status === "deactivated";

  const request = (status: UserStatus) => {
    if (isSelf && (status === "suspended" || status === "deactivated")) {
      toast.problem(
        "You cannot change your own access",
        "An operator cannot suspend or deactivate their own account. Ask another administrator to make the change.",
      );
      return;
    }
    setDialogError(null);
    setPending(status);
  };

  const confirm = async () => {
    if (!pending) return;
    setDialogError(null);
    try {
      const updated = await mutation.mutateAsync(pending);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["user", user.id] }),
        queryClient.invalidateQueries({ queryKey: ["users"] }),
        queryClient.invalidateQueries({ queryKey: ["dashboard"] }),
        queryClient.invalidateQueries({ queryKey: ["audit"] }),
      ]);
      toast.done(
        `User ${statusVerb(pending)}`,
        `${updated.name} is now ${updated.status}. The directory and the audit record show the change.`,
      );
      setPending(null);
    } catch (e) {
      setDialogError(e instanceof Error ? e.message : "The change was refused.");
    }
  };

  const copy = pending ? transitionCopy(user, pending) : null;

  return (
    <>
      <div className="flex flex-wrap items-center gap-1.5">
        {canReactivate ? (
          <Button variant="outline" icon={<RotateCcw size={13} />} onClick={() => request("active")}>
            Reactivate user
          </Button>
        ) : null}
        {canSuspend && !isSelf ? (
          <Button variant="outline" icon={<Ban size={13} />} onClick={() => request("suspended")}>
            Suspend user
          </Button>
        ) : null}
        {canDeactivate && !isSelf ? (
          <Button variant="outline" icon={<UserX size={13} />} onClick={() => request("deactivated")}>
            Deactivate user
          </Button>
        ) : null}
      </div>

      {isSelf && (canSuspend || canDeactivate) ? (
        <p className="text-[0.6875rem] text-ink-muted">
          You are signed in as {user.name}. An operator cannot suspend or deactivate their own account — ask another
          administrator to change it.
        </p>
      ) : null}

      {copy ? (
        <ConfirmDialog
          open
          onClose={() => {
            if (!mutation.isPending) {
              setPending(null);
              setDialogError(null);
            }
          }}
          onConfirm={() => void confirm()}
          title={copy.title}
          description={copy.description}
          confirmLabel={copy.confirmLabel}
          busy={mutation.isPending}
          error={dialogError ?? undefined}
        />
      ) : null}
    </>
  );
}

function EditPanel({ user }: { user: User }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const [name, setName] = useState(user.name);
  const [email, setEmail] = useState(user.email);
  const [mfa, setMfa] = useState(user.mfa);
  const [error, setError] = useState<{ name?: string; email?: string }>({});

  const mutation = useMutation({
    mutationFn: (input: { name: string; email: string; mfa: boolean }) => updateUser(user.id, input),
  });

  const dirty = name !== user.name || email !== user.email || mfa !== user.mfa;

  const submit = async () => {
    setError({});
    try {
      await mutation.mutateAsync({ name, email, mfa });
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["user", user.id] }),
        queryClient.invalidateQueries({ queryKey: ["users"] }),
        queryClient.invalidateQueries({ queryKey: ["dashboard"] }),
        queryClient.invalidateQueries({ queryKey: ["audit"] }),
      ]);
      toast.done("Changes saved", `${name.trim()} was updated. The directory and the audit record now show it.`);
    } catch (e) {
      const message = e instanceof Error ? e.message : "The change could not be saved.";
      // A duplicate or malformed email is a field conflict: the message lands on the email input.
      if (message.toLowerCase().includes("email") || message.includes("@")) setError({ email: message });
      else setError({ name: message });
    }
  };

  return (
    <Panel>
      <PanelHead
        title="Edit profile"
        description="Change the display name, the email or the MFA requirement. Every change is recorded in the audit record."
      />
      <form
        className="flex flex-col gap-3 px-3 py-3"
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        <TextInput
          label="Display name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          error={error.name}
          autoComplete="off"
        />
        <TextInput
          label="Work email"
          type="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          error={error.email}
          autoComplete="off"
        />
        <Checkbox
          label="Require multi-factor authentication"
          hint="The user signs in with a second factor."
          checked={mfa}
          onChange={setMfa}
        />
        <div className="flex flex-wrap items-center gap-2">
          <Button type="submit" variant="primary" busy={mutation.isPending} disabled={!dirty}>
            Save changes
          </Button>
          {dirty ? <span className="text-[0.6875rem] text-ink-muted">Unsaved changes</span> : null}
        </div>
      </form>
    </Panel>
  );
}

function RolesPanel({ roles }: { roles: Role[] }) {
  return (
    <Panel>
      <PanelHead title="Roles" count={roles.length} />
      {roles.length === 0 ? (
        <p className="px-3 py-3 text-body text-ink-muted">
          No roles assigned. This account can sign in but cannot open any section of the board until it is given a role.
        </p>
      ) : (
        <ul className="divide-y divide-rule">
          {roles.map((role) => (
            <li key={role.id} className="flex flex-col gap-0.5 px-3 py-2">
              <span className="flex flex-wrap items-center gap-2">
                <Link
                  to="/roles/$roleId"
                  params={{ roleId: role.id }}
                  className="text-body font-semibold text-ink hover:underline"
                >
                  {role.name}
                </Link>
                <Badge tone="tint">{role.system ? "System" : "Custom"}</Badge>
              </span>
              <span className="text-[0.6875rem] text-ink-muted">{role.description}</span>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}

function MembershipsPanel({ memberships }: { memberships: (Membership & { organization: Organization })[] }) {
  return (
    <Panel>
      <PanelHead title="Organization memberships" count={memberships.length} />
      {memberships.length === 0 ? (
        <p className="px-3 py-3 text-body text-ink-muted">Not a member of any organization.</p>
      ) : (
        <ul className="divide-y divide-rule">
          {memberships.map((membership) => (
            <li key={membership.orgId} className="flex items-center gap-2 px-3 py-2">
              <span className="min-w-0">
                <Link
                  to="/organizations/$orgId"
                  params={{ orgId: membership.organization.id }}
                  className="block truncate text-body font-semibold text-ink hover:underline"
                  title={membership.organization.name}
                >
                  {membership.organization.name}
                </Link>
                <span className="block text-[0.6875rem] text-ink-muted">
                  Joined {absoluteDate(membership.joinedAt)}
                </span>
              </span>
              <span className="ml-auto shrink-0">
                <StatusMagnet status={membership.memberRole} />
              </span>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}

function ActivityPanel({ activity }: { activity: AuditEvent[] }) {
  return (
    <Panel>
      <PanelHead
        title="Recent activity"
        count={activity.length}
        description="Every event this account acted on or was the target of. Open an event for its field-level detail."
      />
      {activity.length === 0 ? (
        <p className="px-3 py-3 text-body text-ink-muted">No recorded activity for this account yet.</p>
      ) : (
        <ul className="divide-y divide-rule">
          {activity.map((event) => (
            <li key={event.id}>
              <Link
                to="/audit/$eventId"
                params={{ eventId: event.id }}
                className="flex flex-col gap-0.5 px-3 py-2 hover:bg-hover"
              >
                <span className="flex items-center gap-2">
                  <span className="font-mono text-[0.6875rem] text-ink">{event.action}</span>
                  <span className="num ml-auto text-[0.6875rem] text-ink-muted">{relativeTime(event.at)}</span>
                </span>
                <span
                  className="truncate text-[0.6875rem] text-ink-muted"
                  title={`${event.actorName} · ${event.targetLabel}`}
                >
                  {event.actorName} · {event.targetLabel}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}
