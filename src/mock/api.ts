/** The mock API layer: the only data source in the panel.
 *
 *  Everything the interface reads or writes goes through this module, with the shape a real HTTP
 *  service would have (async, typed, paginated, failures it can throw). Replacing it with a real
 *  backend is a change to this file and nothing else — no screen imports the seed or the store.
 *
 *  Guarantees this module owns:
 *   - mutual state: one store, one writer, persisted to browser storage across reloads;
 *   - exactly one audit event per state-changing operation, with field-level before/after;
 *   - simulated latency, and failures that are opt-in rather than random, so an empty list is
 *     never confused with a failed request. */

import { isAdministrationRole, permissionUnion, PERMISSION_IDS } from "./permissions";
import { SCHEMA_VERSION, seed, type Database } from "./seed";
import { API_SCOPES, FLAG_ENVIRONMENTS } from "./types";
import {
  ApiError,
  type ApiKey,
  type ApiKeyInput,
  type AuditEvent,
  type DashboardSummary,
  type FeatureFlag,
  type FeatureFlagInput,
  type FieldChange,
  type Identity,
  type ListQuery,
  type Membership,
  type MembershipRole,
  type Organization,
  type Page,
  type Role,
  type Session,
  type User,
  type UserStatus,
} from "./types";

const STORAGE_KEY = "fleet-console.db";
const FAILURE_KEY = "fleet-console.failure";
const DAY = 86_400_000;
export const PAGE_SIZES = [10, 25, 50] as const;
export const DEFAULT_PAGE_SIZE = 25;

export type FailureMode = "off" | "next" | "always";

interface Runtime {
  latencyMs: number;
  failure: FailureMode;
  actor: { id: string; name: string };
}

/** The failure switch has to survive a page reload: the natural way a reviewer checks an error state
 *  is to arm the switch and reload, and a flag held only in module memory resets exactly then — the
 *  switch would appear to do nothing at all. It is per-tab (sessionStorage), not per-machine. */
function storedFailure(): FailureMode {
  try {
    const raw = sessionStorage.getItem(FAILURE_KEY);
    return raw === "next" || raw === "always" ? raw : "off";
  } catch {
    return "off";
  }
}

const runtime: Runtime = {
  latencyMs: 160,
  failure: storedFailure(),
  actor: { id: "usr_owner", name: "Operator" },
};

let store: Database | null = null;

function nowMs(): number {
  return Date.now();
}

function readStore(): Database {
  if (store) return store;
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as Database;
      if (parsed?.version === SCHEMA_VERSION && Array.isArray(parsed.users)) {
        store = parsed;
        return store;
      }
    }
  } catch {
    // a corrupt or unreadable store is treated as empty and reseeded deterministically
  }
  store = seed(nowMs());
  persist();
  return store;
}

function persist(): void {
  if (!store) return;
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(store));
  } catch {
    // storage full or unavailable: the panel keeps working in memory for this session
  }
}

function mutate<T>(fn: (db: Database) => T): T {
  const db = readStore();
  const result = fn(db);
  persist();
  return result;
}

/* ------------------------------------------------------------------ runtime controls */

export function configure(options: { latencyMs?: number; failure?: FailureMode }): void {
  if (options.latencyMs !== undefined) runtime.latencyMs = options.latencyMs;
  if (options.failure !== undefined) {
    runtime.failure = options.failure;
    try {
      sessionStorage.setItem(FAILURE_KEY, options.failure);
    } catch {
      // storage unavailable: the switch still applies for this session
    }
  }
}

export function currentRuntime(): Runtime {
  return { ...runtime, actor: { ...runtime.actor } };
}

export function setActor(identity: Identity | null): void {
  runtime.actor = identity
    ? { id: identity.userId, name: identity.name }
    : { id: "system", name: "System" };
}

export function databaseInfo(): { seededAt: string; version: number; records: number } {
  const db = readStore();
  return {
    seededAt: db.seededAt,
    version: db.version,
    records:
      db.users.length +
      db.roles.length +
      db.organizations.length +
      db.sessions.length +
      db.audit.length +
      db.memberships.length,
  };
}

export function resetDatabase(): void {
  store = seed(nowMs());
  persist();
}

/** Point the board's "current session" at the signed-in account, creating a session row when the
 *  account has none, so the interface can always mark which session is the operator's own. */
export async function bindCurrentSession(userId: string): Promise<void> {
  await gate();
  return mutate((db) => {
    const existing = db.sessions.find((s) => s.userId === userId);
    if (existing) {
      db.currentSessionId = existing.id;
      return;
    }
    const session: Session = {
      id: nextId("ses", db.sessions),
      userId,
      device: navigator.userAgent.includes("Mac") ? "MacBook Pro" : "Windows Desktop",
      browser: navigator.userAgent.includes("Firefox") ? "Firefox 133" : "Chrome 141",
      location: "Hanoi, VN",
      ip: "203.0.113.24",
      startedAt: new Date(nowMs()).toISOString(),
      lastSeenAt: new Date(nowMs()).toISOString(),
      current: true,
    };
    db.sessions = [session, ...db.sessions];
    db.currentSessionId = session.id;
  });
}

async function gate(): Promise<void> {
  if (runtime.latencyMs > 0) {
    await new Promise((resolve) => setTimeout(resolve, runtime.latencyMs));
  }
  if (runtime.failure === "always" || runtime.failure === "next") {
    const wasNext = runtime.failure === "next";
    if (wasNext) {
      runtime.failure = "off";
      try {
        sessionStorage.setItem(FAILURE_KEY, "off");
      } catch {
        // storage unavailable: nothing to clear
      }
    }
    throw new ApiError(
      wasNext ? "server" : "offline",
      wasNext
        ? "The service returned an error (simulated). Nothing was changed."
        : "The service is unreachable (simulated). Nothing was changed.",
    );
  }
}

/* ------------------------------------------------------------------ helpers */

function nextId(prefix: string, existing: { id: string }[]): string {
  const highest = existing.reduce((max, item) => {
    const n = Number.parseInt(item.id.replace(/\D/g, ""), 10);
    return Number.isFinite(n) && n > max ? n : max;
  }, 0);
  return `${prefix}_${String(highest + 1).padStart(4, "0")}`;
}

function compare(a: string | number | null, b: string | number | null): number {
  if (a === b) return 0;
  if (a === null) return 1;
  if (b === null) return -1;
  return a < b ? -1 : 1;
}

function paginate<T>(rows: T[], query: ListQuery): Page<T> {
  const pageSize = PAGE_SIZES.includes(query.pageSize as (typeof PAGE_SIZES)[number])
    ? (query.pageSize as number)
    : DEFAULT_PAGE_SIZE;
  const total = rows.length;
  const maxPage = Math.max(1, Math.ceil(total / pageSize));
  const page = Math.min(Math.max(1, query.page ?? 1), maxPage);
  return { items: rows.slice((page - 1) * pageSize, page * pageSize), total, page, pageSize };
}

function sortRows<T>(rows: T[], sort: string | undefined, dir: "asc" | "desc", key: (row: T, field: string) => string | number | null): T[] {
  if (!sort) return rows;
  const sorted = [...rows].sort((a, b) => compare(key(a, sort), key(b, sort)));
  return dir === "desc" ? sorted.reverse() : sorted;
}

function appendEvent(
  db: Database,
  input: {
    action: string;
    targetType: AuditEvent["targetType"];
    targetId: string;
    targetLabel: string;
    changes?: FieldChange[];
  },
): AuditEvent {
  const event: AuditEvent = {
    id: nextId("evt", db.audit),
    at: new Date(nowMs()).toISOString(),
    actorId: runtime.actor.id,
    actorName: runtime.actor.name,
    action: input.action,
    targetType: input.targetType,
    targetId: input.targetId,
    targetLabel: input.targetLabel,
    ip: "203.0.113.24",
    changes: input.changes ?? [],
  };
  db.audit = [event, ...db.audit];
  return event;
}

function requireUser(db: Database, id: string): User {
  const user = db.users.find((u) => u.id === id);
  if (!user) throw new ApiError("not_found", `No user ${id}. The record may have been removed.`);
  return user;
}

function permissionsOf(db: Database, roleIds: string[]): string[] {
  return permissionUnion(roleIds, db.roles);
}

/** The rule that keeps the workspace administrable: at least one ACTIVE user must hold a role
 *  that grants administration, or the check throws before the change lands. */
function assertAdministrationRemains(db: Database, nextUsers: User[]): void {
  const administrators = nextUsers.filter(
    (u) => u.status === "active" && isAdministrationRole(permissionsOf(db, u.roleIds)),
  );
  if (administrators.length === 0) {
    throw new ApiError(
      "conflict",
      "This would leave the workspace with no active administrator. Grant an administration role first.",
    );
  }
}

/* ------------------------------------------------------------------ reads */

export async function listUsers(query: ListQuery = {}): Promise<Page<User>> {
  await gate();
  let rows = [...readStore().users];
  const q = query.q?.trim().toLowerCase();
  if (q) rows = rows.filter((u) => u.name.toLowerCase().includes(q) || u.email.toLowerCase().includes(q));
  if (query.status) rows = rows.filter((u) => u.status === query.status);
  if (query.roleId) rows = rows.filter((u) => u.roleIds.includes(query.roleId!));
  if (query.orgId) rows = rows.filter((u) => u.orgIds.includes(query.orgId!));
  rows = sortRows(rows, query.sort, query.dir ?? "asc", (u, field) => {
    if (field === "status") return u.status;
    if (field === "lastActiveAt") return u.lastActiveAt ? 0 : 1;
    if (field === "lastActiveAtRaw") return u.lastActiveAt;
    if (field === "createdAt") return u.createdAt;
    if (field === "email") return u.email;
    return u.name;
  });
  if (query.sort === "lastActiveAt") {
    rows.sort((a, b) => compare(b.lastActiveAt ?? "", a.lastActiveAt ?? ""));
    if (query.dir === "asc") rows.reverse();
  }
  return paginate(rows, query);
}

export async function getUser(id: string): Promise<{
  user: User;
  roles: Role[];
  memberships: (Membership & { organization: Organization })[];
  activity: AuditEvent[];
}> {
  await gate();
  const db = readStore();
  const user = requireUser(db, id);
  return {
    user,
    roles: db.roles.filter((r) => user.roleIds.includes(r.id)),
    memberships: db.memberships
      .filter((m) => m.userId === id)
      .map((m) => ({ ...m, organization: db.organizations.find((o) => o.id === m.orgId)! }))
      .filter((m) => Boolean(m.organization)),
    activity: db.audit.filter((e) => e.targetId === id || e.actorId === id).slice(0, 12),
  };
}

export async function listRoles(): Promise<(Role & { memberCount: number })[]> {
  await gate();
  const db = readStore();
  return db.roles.map((role) => ({
    ...role,
    memberCount: db.users.filter((u) => u.roleIds.includes(role.id)).length,
  }));
}

export async function getRole(id: string): Promise<{ role: Role; members: User[] }> {
  await gate();
  const db = readStore();
  const role = db.roles.find((r) => r.id === id);
  if (!role) throw new ApiError("not_found", `No role ${id}.`);
  return { role, members: db.users.filter((u) => u.roleIds.includes(id)) };
}

export async function listOrganizations(query: ListQuery = {}): Promise<Page<Organization & { memberCount: number }>> {
  await gate();
  const db = readStore();
  let rows = db.organizations.map((o) => ({
    ...o,
    memberCount: db.memberships.filter((m) => m.orgId === o.id).length,
  }));
  const q = query.q?.trim().toLowerCase();
  if (q) rows = rows.filter((o) => o.name.toLowerCase().includes(q) || o.slug.includes(q));
  rows = sortRows(rows, query.sort, query.dir ?? "asc", (o, field) => {
    if (field === "plan") return o.plan;
    if (field === "status") return o.status;
    if (field === "memberCount") return o.memberCount;
    if (field === "createdAt") return o.createdAt;
    return o.name;
  });
  return paginate(rows, query);
}

export async function getOrganization(id: string): Promise<{
  organization: Organization;
  members: (Membership & { user: User })[];
}> {
  await gate();
  const db = readStore();
  const organization = db.organizations.find((o) => o.id === id);
  if (!organization) throw new ApiError("not_found", `No organization ${id}.`);
  const members = db.memberships
    .filter((m) => m.orgId === id)
    .map((m) => ({ ...m, user: db.users.find((u) => u.id === m.userId)! }))
    .filter((m) => Boolean(m.user));
  return { organization, members };
}

export async function listSessions(query: ListQuery = {}): Promise<Page<Session & { user: User | undefined }>> {
  await gate();
  const db = readStore();
  let rows = db.sessions;
  if (query.orgId) {
    const inOrg = new Set(db.users.filter((u) => u.orgIds.includes(query.orgId!)).map((u) => u.id));
    rows = rows.filter((s) => inOrg.has(s.userId));
  }
  const q = query.q?.trim().toLowerCase();
  if (q) {
    rows = rows.filter((s) => {
      const user = db.users.find((u) => u.id === s.userId);
      return (
        s.device.toLowerCase().includes(q) ||
        s.location.toLowerCase().includes(q) ||
        s.ip.toLowerCase().includes(q) ||
        (user?.name.toLowerCase().includes(q) ?? false) ||
        (user?.email.toLowerCase().includes(q) ?? false)
      );
    });
  }
  const withUser = rows.map((s) => ({ ...s, current: s.id === db.currentSessionId, user: db.users.find((u) => u.id === s.userId) }));
  const sorted = sortRows(withUser, query.sort, query.dir ?? "desc", (s, field) => {
    if (field === "user") return s.user?.name ?? "";
    if (field === "device") return s.device;
    if (field === "location") return s.location;
    if (field === "lastSeenAt") return s.lastSeenAt;
    return s.startedAt;
  });
  return paginate(sorted, query);
}


export async function dashboardSummary(orgId?: string): Promise<DashboardSummary> {
  await gate();
  const db = readStore();
  const end = nowMs();
  const start = end - 30 * DAY;
  const previousStart = start - 30 * DAY;

  const scopedUsers = orgId ? db.users.filter((u) => u.orgIds.includes(orgId)) : db.users;
  const userIds = new Set(scopedUsers.map((u) => u.id));
  const scopedSessions = orgId ? db.sessions.filter((s) => userIds.has(s.userId)) : db.sessions;
  const scopedEvents = orgId
    ? db.audit.filter((e) => userIds.has(e.actorId) || userIds.has(e.targetId))
    : db.audit;

  const inPeriod = (iso: string, from: number, to: number) => {
    const t = new Date(iso).getTime();
    return t >= from && t <= to;
  };

  const usersThis = scopedUsers.filter((u) => inPeriod(u.createdAt, start, end)).length;
  const usersPrev = scopedUsers.filter((u) => inPeriod(u.createdAt, previousStart, start)).length;
  const sessionsThis = scopedSessions.filter((s) => inPeriod(s.startedAt, start, end)).length;
  const sessionsPrev = scopedSessions.filter((s) => inPeriod(s.startedAt, previousStart, start)).length;
  const orgsThis = db.organizations.filter((o) => inPeriod(o.createdAt, start, end)).length;
  const orgsPrev = db.organizations.filter((o) => inPeriod(o.createdAt, previousStart, start)).length;
  const eventsThis = scopedEvents.filter((e) => inPeriod(e.at, start, end)).length;
  const eventsPrev = scopedEvents.filter((e) => inPeriod(e.at, previousStart, start)).length;

  return {
    period: { from: new Date(start).toISOString(), to: new Date(end).toISOString() },
    users: {
      total: scopedUsers.length,
      active: scopedUsers.filter((u) => u.status === "active").length,
      invited: scopedUsers.filter((u) => u.status === "invited").length,
      suspended: scopedUsers.filter((u) => u.status === "suspended").length,
      delta: usersThis - usersPrev,
    },
    sessions: {
      total: scopedSessions.length,
      other: scopedSessions.filter((s) => s.id !== db.currentSessionId).length,
      delta: sessionsThis - sessionsPrev,
    },
    organizations: { total: db.organizations.length, delta: orgsThis - orgsPrev },
    events: { total: eventsThis, delta: eventsThis - eventsPrev },
    recent: scopedEvents.slice(0, 8),
  };
}

/* ------------------------------------------------------------------ writes */

function diff(changes: FieldChange[]): FieldChange[] {
  return changes.filter((c) => c.before !== c.after);
}

export async function createUser(input: {
  name: string;
  email: string;
  roleIds: string[];
  orgIds: string[];
  status?: UserStatus;
}): Promise<User> {
  await gate();
  return mutate((db) => {
    const email = input.email.trim().toLowerCase();
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) {
      throw new ApiError("conflict", `“${input.email}” is not a valid email address.`);
    }
    if (db.users.some((u) => u.email.toLowerCase() === email)) {
      throw new ApiError("conflict", `${email} already belongs to a user on this board.`);
    }
    if (input.name.trim().length < 2) throw new ApiError("conflict", "A display name is required.");
    const user: User = {
      id: nextId("usr", db.users),
      name: input.name.trim(),
      email,
      status: input.status ?? db.settings.defaultUserStatus,
      roleIds: input.roleIds.length > 0 ? input.roleIds : [],
      orgIds: input.orgIds,
      mfa: false,
      createdAt: new Date(nowMs()).toISOString(),
      lastActiveAt: null,
    };
    db.users = [user, ...db.users];
    for (const orgId of input.orgIds) {
      db.memberships = [...db.memberships, { orgId, userId: user.id, memberRole: "member", joinedAt: user.createdAt }];
    }
    appendEvent(db, {
      action: user.status === "invited" ? "user.invited" : "user.created",
      targetType: "user",
      targetId: user.id,
      targetLabel: user.name,
      changes: diff([
        { field: "email", before: null, after: user.email },
        { field: "status", before: null, after: user.status },
        { field: "roles", before: null, after: user.roleIds.join(", ") || "none" },
      ]),
    });
    return user;
  });
}

export async function updateUser(
  id: string,
  input: { name?: string; email?: string; roleIds?: string[]; mfa?: boolean },
): Promise<User> {
  await gate();
  return mutate((db) => {
    const user = requireUser(db, id);
    const changes: FieldChange[] = [];
    const next: User = { ...user };

    if (input.name !== undefined && input.name.trim() !== user.name) {
      if (input.name.trim().length < 2) throw new ApiError("conflict", "A display name is required.");
      changes.push({ field: "name", before: user.name, after: input.name.trim() });
      next.name = input.name.trim();
    }
    if (input.email !== undefined) {
      const email = input.email.trim().toLowerCase();
      if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) {
        throw new ApiError("conflict", `“${input.email}” is not a valid email address.`);
      }
      if (db.users.some((u) => u.id !== id && u.email.toLowerCase() === email)) {
        throw new ApiError("conflict", `${email} already belongs to a user on this board.`);
      }
      if (email !== user.email) {
        changes.push({ field: "email", before: user.email, after: email });
        next.email = email;
      }
    }
    if (input.mfa !== undefined && input.mfa !== user.mfa) {
      changes.push({ field: "mfa", before: user.mfa ? "enabled" : "disabled", after: input.mfa ? "enabled" : "disabled" });
      next.mfa = input.mfa;
    }
    if (input.roleIds) {
      const sorted = [...input.roleIds].sort();
      if (sorted.join(",") !== [...user.roleIds].sort().join(",")) {
        const nextUsers = db.users.map((u) => (u.id === id ? { ...next, roleIds: sorted } : u));
        assertAdministrationRemains(db, nextUsers);
        changes.push({ field: "roles", before: user.roleIds.join(", "), after: sorted.join(", ") });
        next.roleIds = sorted;
      }
    }
    if (changes.length === 0) return user;
    db.users = db.users.map((u) => (u.id === id ? next : u));
    appendEvent(db, {
      action: "user.updated",
      targetType: "user",
      targetId: next.id,
      targetLabel: next.name,
      changes: diff(changes),
    });
    return next;
  });
}

export async function setUserStatus(id: string, status: UserStatus): Promise<User> {
  await gate();
  return mutate((db) => {
    const user = requireUser(db, id);
    if (user.status === status) return user;
    const next: User = { ...user, status };
    const nextUsers = db.users.map((u) => (u.id === id ? next : u));
    assertAdministrationRemains(db, nextUsers);
    db.users = nextUsers;
    if (status === "deactivated") {
      db.sessions = db.sessions.filter((s) => s.userId !== id);
    }
    appendEvent(db, {
      action: status === "suspended" ? "user.suspended" : status === "active" ? "user.reactivated" : `user.${status}`,
      targetType: "user",
      targetId: id,
      targetLabel: user.name,
      changes: [{ field: "status", before: user.status, after: status }],
    });
    return next;
  });
}

export async function bulkSetUserStatus(ids: string[], status: UserStatus): Promise<{ changed: number; skipped: number }> {
  await gate();
  return mutate((db) => {
    const targets = db.users.filter((u) => ids.includes(u.id));
    const applicable = targets.filter((u) => u.status !== status);
    const skipped = ids.length - applicable.length;
    if (applicable.length === 0) return { changed: 0, skipped };
    const nextUsers = db.users.map((u) => (applicable.some((a) => a.id === u.id) ? { ...u, status } : u));
    assertAdministrationRemains(db, nextUsers);
    db.users = nextUsers;
    if (status === "deactivated") {
      db.sessions = db.sessions.filter((s) => !applicable.some((a) => a.id === s.userId));
    }
    for (const user of applicable) {
      appendEvent(db, {
        action: `user.${status}`,
        targetType: "user",
        targetId: user.id,
        targetLabel: user.name,
        changes: [{ field: "status", before: user.status, after: status }],
      });
    }
    return { changed: applicable.length, skipped };
  });
}

export async function createRole(input: { name: string; description: string; permissions: string[] }): Promise<Role> {
  await gate();
  return mutate((db) => {
    const name = input.name.trim();
    if (name.length < 2) throw new ApiError("conflict", "A role name is required.");
    if (db.roles.some((r) => r.name.toLowerCase() === name.toLowerCase())) {
      throw new ApiError("conflict", `A role called “${name}” already exists.`);
    }
    const role: Role = {
      id: nextId("role", db.roles),
      name,
      description: input.description.trim(),
      system: false,
      permissions: input.permissions.filter((p) => PERMISSION_IDS.includes(p)),
      createdAt: new Date(nowMs()).toISOString(),
    };
    db.roles = [...db.roles, role];
    appendEvent(db, {
      action: "role.created",
      targetType: "role",
      targetId: role.id,
      targetLabel: role.name,
      changes: [{ field: "permissions", before: null, after: `${role.permissions.length} granted` }],
    });
    return role;
  });
}

export async function updateRole(
  id: string,
  input: { name?: string; description?: string; permissions?: string[] },
): Promise<Role> {
  await gate();
  return mutate((db) => {
    const role = db.roles.find((r) => r.id === id);
    if (!role) throw new ApiError("not_found", `No role ${id}.`);
    const next: Role = { ...role };
    const changes: FieldChange[] = [];

    if (input.name !== undefined && input.name.trim() !== role.name) {
      const name = input.name.trim();
      if (name.length < 2) throw new ApiError("conflict", "A role name is required.");
      if (db.roles.some((r) => r.id !== id && r.name.toLowerCase() === name.toLowerCase())) {
        throw new ApiError("conflict", `A role called “${name}” already exists.`);
      }
      changes.push({ field: "name", before: role.name, after: name });
      next.name = name;
    }
    if (input.description !== undefined && input.description !== role.description) {
      changes.push({ field: "description", before: role.description, after: input.description });
      next.description = input.description;
    }
    if (input.permissions) {
      const permissions = input.permissions.filter((p) => PERMISSION_IDS.includes(p));
      const before = [...role.permissions].sort().join(",");
      const after = [...permissions].sort().join(",");
      if (before !== after) {
        const nextRoles = db.roles.map((r) => (r.id === id ? { ...next, permissions } : r));
        const administrators = db.users.filter(
          (u) => u.status === "active" && isAdministrationRole(permissionUnion(u.roleIds, nextRoles)),
        );
        if (administrators.length === 0) {
          throw new ApiError(
            "conflict",
            "This change would leave no role able to administer the workspace. Grant administration elsewhere first.",
          );
        }
        changes.push({
          field: "permissions",
          before: `${role.permissions.length} granted`,
          after: `${permissions.length} granted`,
        });
        next.permissions = permissions;
      }
    }
    if (changes.length === 0) return role;
    db.roles = db.roles.map((r) => (r.id === id ? next : r));
    appendEvent(db, {
      action: "role.updated",
      targetType: "role",
      targetId: next.id,
      targetLabel: next.name,
      changes: diff(changes),
    });
    return next;
  });
}

export async function deleteRole(id: string): Promise<void> {
  await gate();
  return mutate((db) => {
    const role = db.roles.find((r) => r.id === id);
    if (!role) throw new ApiError("not_found", `No role ${id}.`);
    if (role.system) {
      throw new ApiError("conflict", `${role.name} is a system role and cannot be deleted.`);
    }
    const holders = db.users.filter((u) => u.roleIds.includes(id));
    if (holders.length > 0) {
      throw new ApiError("conflict", `${holders.length} users still hold ${role.name}. Move them before deleting it.`);
    }
    const remaining = db.roles.filter((r) => r.id !== id);
    const administrators = db.users.filter(
      (u) => u.status === "active" && isAdministrationRole(permissionUnion(u.roleIds, remaining)),
    );
    if (administrators.length === 0) {
      throw new ApiError("conflict", "This is the last role that can administer the workspace, so it cannot be deleted.");
    }
    db.roles = remaining;
    appendEvent(db, {
      action: "role.deleted",
      targetType: "role",
      targetId: role.id,
      targetLabel: role.name,
      changes: [{ field: "state", before: "present", after: "deleted" }],
    });
  });
}

export async function addOrganizationMember(
  orgId: string,
  userId: string,
  memberRole: MembershipRole,
): Promise<Membership> {
  await gate();
  return mutate((db) => {
    const org = db.organizations.find((o) => o.id === orgId);
    if (!org) throw new ApiError("not_found", `No organization ${orgId}.`);
    const user = requireUser(db, userId);
    if (db.memberships.some((m) => m.orgId === orgId && m.userId === userId)) {
      throw new ApiError("conflict", `${user.name} is already a member of ${org.name}.`);
    }
    const membership: Membership = { orgId, userId, memberRole, joinedAt: new Date(nowMs()).toISOString() };
    db.memberships = [...db.memberships, membership];
    db.users = db.users.map((u) => (u.id === userId ? { ...u, orgIds: [...new Set([...u.orgIds, orgId])] } : u));
    appendEvent(db, {
      action: "organization.member_added",
      targetType: "organization",
      targetId: orgId,
      targetLabel: org.name,
      changes: [{ field: "member", before: null, after: `${user.name} (${memberRole})` }],
    });
    return membership;
  });
}

export async function updateOrganizationMember(orgId: string, userId: string, memberRole: MembershipRole): Promise<void> {
  await gate();
  return mutate((db) => {
    const membership = db.memberships.find((m) => m.orgId === orgId && m.userId === userId);
    if (!membership) throw new ApiError("not_found", "That user is not a member of this organization.");
    const org = db.organizations.find((o) => o.id === orgId);
    const user = requireUser(db, userId);
    if (membership.memberRole === memberRole) return;
    if (membership.memberRole === "owner") {
      const owners = db.memberships.filter((m) => m.orgId === orgId && m.memberRole === "owner");
      if (owners.length <= 1) {
        throw new ApiError("conflict", `${user.name} is the last owner of ${org?.name ?? "this organization"}. Promote someone else first.`);
      }
    }
    db.memberships = db.memberships.map((m) =>
      m.orgId === orgId && m.userId === userId ? { ...m, memberRole } : m,
    );
    appendEvent(db, {
      action: "organization.member_role_changed",
      targetType: "organization",
      targetId: orgId,
      targetLabel: org?.name ?? orgId,
      changes: [{ field: `${user.name} role`, before: membership.memberRole, after: memberRole }],
    });
  });
}

export async function removeOrganizationMember(orgId: string, userId: string): Promise<void> {
  await gate();
  return mutate((db) => {
    const membership = db.memberships.find((m) => m.orgId === orgId && m.userId === userId);
    if (!membership) throw new ApiError("not_found", "That user is not a member of this organization.");
    const org = db.organizations.find((o) => o.id === orgId);
    const user = requireUser(db, userId);
    if (membership.memberRole === "owner") {
      const owners = db.memberships.filter((m) => m.orgId === orgId && m.memberRole === "owner");
      if (owners.length <= 1) {
        throw new ApiError("conflict", `${user.name} is the last owner of ${org?.name ?? "this organization"}, so they cannot be removed.`);
      }
    }
    db.memberships = db.memberships.filter((m) => !(m.orgId === orgId && m.userId === userId));
    db.users = db.users.map((u) => (u.id === userId ? { ...u, orgIds: u.orgIds.filter((o) => o !== orgId) } : u));
    appendEvent(db, {
      action: "organization.member_removed",
      targetType: "organization",
      targetId: orgId,
      targetLabel: org?.name ?? orgId,
      changes: [{ field: "member", before: user.name, after: null }],
    });
  });
}

export async function revokeSession(id: string): Promise<void> {
  await gate();
  return mutate((db) => {
    const session = db.sessions.find((s) => s.id === id);
    if (!session) throw new ApiError("not_found", `Session ${id} is already gone.`);
    const user = db.users.find((u) => u.id === session.userId);
    db.sessions = db.sessions.filter((s) => s.id !== id);
    appendEvent(db, {
      action: "session.revoked",
      targetType: "session",
      targetId: id,
      targetLabel: `${session.device} · ${session.location}`,
      changes: [
        { field: "state", before: "active", after: "revoked" },
        { field: "held by", before: user?.name ?? session.userId, after: user?.name ?? session.userId },
      ],
    });
  });
}

export async function revokeOtherSessions(): Promise<{ revoked: number }> {
  await gate();
  return mutate((db) => {
    const others = db.sessions.filter((s) => s.id !== db.currentSessionId);
    if (others.length === 0) return { revoked: 0 };
    db.sessions = db.sessions.filter((s) => s.id === db.currentSessionId);
    appendEvent(db, {
      action: "session.revoked_others",
      targetType: "session",
      targetId: db.currentSessionId,
      targetLabel: `${others.length} other sessions`,
      changes: [{ field: "active sessions", before: String(others.length + 1), after: "1" }],
    });
    return { revoked: others.length };
  });
}

/* ------------------------------------------------------------------ API keys
 * The write path for the dialog CRUD surface. The secret exists only in the return value of
 * `createApiKey`: the store keeps a fingerprint, so no surface can ever show it again. */

function requireApiKey(db: Database, id: string): ApiKey {
  const key = db.apiKeys.find((k) => k.id === id);
  if (!key) throw new ApiError("not_found", `No API key ${id}. It may have been revoked and removed.`);
  return key;
}

function validateApiKeyName(db: Database, name: string, selfId?: string): string {
  const trimmed = name.trim();
  if (trimmed.length < 2) throw new ApiError("conflict", "A key name of at least two characters is required.");
  if (db.apiKeys.some((k) => k.id !== selfId && k.name.toLowerCase() === trimmed.toLowerCase())) {
    throw new ApiError("conflict", `A key called “${trimmed}” already exists.`);
  }
  return trimmed;
}

function validateScopes(scopes: string[]): string[] {
  const valid = scopes.filter((s) => API_SCOPES.some((def) => def.id === s));
  if (valid.length === 0) {
    throw new ApiError("conflict", "A key needs at least one scope — a key that may do nothing is not useful.");
  }
  return valid;
}

function generateSecret(environment: ApiKey["environment"]): string {
  const bytes = new Uint8Array(24);
  crypto.getRandomValues(bytes);
  const body = Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
  return `sk_${environment}_${body}`;
}

function fingerprintOf(secret: string): string {
  // Deterministic, non-reversible-enough for a demo: a short digest of the secret, never the secret.
  let h1 = 0x811c9dc5;
  let h2 = 0x1000193;
  for (let i = 0; i < secret.length; i += 1) {
    h1 = (h1 ^ secret.charCodeAt(i)) * 0x01000193;
    h2 = (h2 + secret.charCodeAt(i) * (i + 7)) >>> 0;
  }
  return `fp_${(h1 >>> 0).toString(36)}${h2.toString(36)}`;
}

export async function listApiKeys(query: ListQuery = {}): Promise<Page<ApiKey>> {
  await gate();
  const db = readStore();
  let rows = [...db.apiKeys];
  const q = query.q?.trim().toLowerCase();
  if (q) rows = rows.filter((k) => k.name.toLowerCase().includes(q) || k.lastFour.includes(q));
  if (query.status) rows = rows.filter((k) => k.status === query.status);
  if (query.environment) rows = rows.filter((k) => k.environment === query.environment);
  rows = sortRows(rows, query.sort, query.dir ?? (query.sort === "createdAt" ? "desc" : "asc"), (k, field) => {
    if (field === "lastUsedAt") return k.lastUsedAt ?? "";
    if (field === "createdAt") return k.createdAt;
    if (field === "status") return k.status;
    if (field === "environment") return k.environment;
    return k.name;
  });
  return paginate(rows, query);
}

export async function getApiKey(id: string): Promise<ApiKey> {
  await gate();
  return { ...requireApiKey(readStore(), id) };
}

export async function createApiKey(input: ApiKeyInput): Promise<{ key: ApiKey; secret: string }> {
  await gate();
  return mutate((db) => {
    const name = validateApiKeyName(db, input.name);
    const scopes = validateScopes(input.scopes);
    const secret = generateSecret(input.environment);
    const key: ApiKey = {
      id: nextId("key", db.apiKeys),
      name,
      fingerprint: fingerprintOf(secret),
      lastFour: secret.slice(-4),
      scopes,
      environment: input.environment,
      status: "active",
      createdAt: new Date(nowMs()).toISOString(),
      lastUsedAt: null,
      createdBy: runtime.actor.id,
    };
    db.apiKeys = [key, ...db.apiKeys];
    appendEvent(db, {
      action: "apikey.issued",
      targetType: "api_key",
      targetId: key.id,
      targetLabel: key.name,
      changes: diff([
        { field: "scopes", before: null, after: scopes.join(", ") },
        { field: "environment", before: null, after: key.environment },
        { field: "status", before: null, after: "active" },
      ]),
    });
    return { key, secret };
  });
}

export async function updateApiKey(id: string, input: Partial<ApiKeyInput>): Promise<ApiKey> {
  await gate();
  return mutate((db) => {
    const key = requireApiKey(db, id);
    if (key.status === "revoked") {
      throw new ApiError("conflict", `${key.name} is revoked, so it cannot be edited.`);
    }
    const next: ApiKey = { ...key };
    const changes: FieldChange[] = [];
    if (input.name !== undefined) {
      const name = validateApiKeyName(db, input.name, id);
      if (name !== key.name) {
        changes.push({ field: "name", before: key.name, after: name });
        next.name = name;
      }
    }
    if (input.scopes !== undefined) {
      const scopes = validateScopes(input.scopes);
      if ([...scopes].sort().join(",") !== [...key.scopes].sort().join(",")) {
        changes.push({ field: "scopes", before: key.scopes.join(", "), after: scopes.join(", ") });
        next.scopes = scopes;
      }
    }
    if (input.environment !== undefined && input.environment !== key.environment) {
      changes.push({ field: "environment", before: key.environment, after: input.environment });
      next.environment = input.environment;
    }
    if (changes.length === 0) return key;
    db.apiKeys = db.apiKeys.map((k) => (k.id === id ? next : k));
    appendEvent(db, {
      action: "apikey.updated",
      targetType: "api_key",
      targetId: next.id,
      targetLabel: next.name,
      changes: diff(changes),
    });
    return next;
  });
}

export async function revokeApiKey(id: string): Promise<void> {
  await gate();
  return mutate((db) => {
    const key = requireApiKey(db, id);
    if (key.status === "revoked") {
      throw new ApiError("conflict", `${key.name} has already been revoked.`);
    }
    db.apiKeys = db.apiKeys.map((k) => (k.id === id ? { ...k, status: "revoked" as const } : k));
    appendEvent(db, {
      action: "apikey.revoked",
      targetType: "api_key",
      targetId: key.id,
      targetLabel: key.name,
      changes: [{ field: "status", before: "active", after: "revoked" }],
    });
  });
}

/* ------------------------------------------------------------------ feature flags
 * The write path for the side-panel CRUD surface. */

function requireFlag(db: Database, id: string): FeatureFlag {
  const flag = db.flags.find((f) => f.id === id);
  if (!flag) throw new ApiError("not_found", `No feature flag ${id}.`);
  return flag;
}

function validateFlagKey(db: Database, key: string, selfId?: string): string {
  const trimmed = key.trim();
  if (!/^[a-z0-9]+([.-][a-z0-9]+)*$/.test(trimmed) || trimmed.length < 3) {
    throw new ApiError(
      "conflict",
      `“${trimmed}” is not a valid key. Use lowercase words separated by single hyphens or dots, for example checkout.express-lane.`,
    );
  }
  if (db.flags.some((f) => f.id !== selfId && f.key === trimmed)) {
    throw new ApiError("conflict", `A flag with the key “${trimmed}” already exists.`);
  }
  return trimmed;
}

function validateRollout(rollout: number): number {
  if (!Number.isInteger(rollout) || rollout < 0 || rollout > 100) {
    throw new ApiError("conflict", "Rollout must be a whole number between 0 and 100.");
  }
  return rollout;
}

export async function listFlags(query: ListQuery = {}): Promise<Page<FeatureFlag & { owner: User | undefined }>> {
  await gate();
  const db = readStore();
  let rows = db.flags;
  const q = query.q?.trim().toLowerCase();
  if (q) rows = rows.filter((f) => f.name.toLowerCase().includes(q) || f.key.toLowerCase().includes(q));
  if (query.status) rows = rows.filter((f) => f.state === query.status);
  if (query.environment) rows = rows.filter((f) => f.environments.includes(query.environment!));
  const withOwner = rows.map((f) => ({ ...f, owner: db.users.find((u) => u.id === f.ownerId) }));
  const sorted = sortRows(withOwner, query.sort, query.dir ?? "asc", (f, field) => {
    if (field === "state") return f.state;
    if (field === "rollout") return f.rollout;
    if (field === "updatedAt") return f.updatedAt;
    if (field === "owner") return f.owner?.name ?? "";
    return f.name;
  });
  return paginate(sorted, query);
}

export async function getFlag(id: string): Promise<FeatureFlag> {
  await gate();
  return { ...requireFlag(readStore(), id) };
}

export async function createFlag(input: FeatureFlagInput): Promise<FeatureFlag> {
  await gate();
  return mutate((db) => {
    const key = validateFlagKey(db, input.key);
    const name = input.name.trim();
    if (name.length < 2) throw new ApiError("conflict", "A flag name of at least two characters is required.");
    const rollout = validateRollout(input.rollout);
    const environments = input.environments.filter((e) => FLAG_ENVIRONMENTS.includes(e));
    if (environments.length === 0) {
      throw new ApiError("conflict", "A flag has to apply to at least one environment.");
    }
    const owner = db.users.find((u) => u.id === input.ownerId);
    if (!owner) throw new ApiError("conflict", "Choose an owner from the directory.");
    const now = new Date(nowMs()).toISOString();
    const flag: FeatureFlag = {
      id: nextId("flag", db.flags),
      key,
      name,
      description: input.description.trim(),
      state: input.state,
      rollout,
      environments,
      ownerId: owner.id,
      createdAt: now,
      updatedAt: now,
      updatedBy: runtime.actor.id,
    };
    db.flags = [flag, ...db.flags];
    appendEvent(db, {
      action: "flag.created",
      targetType: "flag",
      targetId: flag.id,
      targetLabel: flag.key,
      changes: diff([
        { field: "state", before: null, after: flag.state },
        { field: "rollout", before: null, after: `${flag.rollout}%` },
        { field: "environments", before: null, after: environments.join(", ") },
      ]),
    });
    return flag;
  });
}

export async function updateFlag(id: string, input: Partial<FeatureFlagInput>): Promise<FeatureFlag> {
  await gate();
  return mutate((db) => {
    const flag = requireFlag(db, id);
    const next: FeatureFlag = { ...flag };
    const changes: FieldChange[] = [];

    if (input.key !== undefined) {
      const key = validateFlagKey(db, input.key, id);
      if (key !== flag.key) {
        changes.push({ field: "key", before: flag.key, after: key });
        next.key = key;
      }
    }
    if (input.name !== undefined) {
      const name = input.name.trim();
      if (name.length < 2) throw new ApiError("conflict", "A flag name of at least two characters is required.");
      if (name !== flag.name) {
        changes.push({ field: "name", before: flag.name, after: name });
        next.name = name;
      }
    }
    if (input.description !== undefined && input.description.trim() !== flag.description) {
      changes.push({ field: "description", before: flag.description, after: input.description.trim() });
      next.description = input.description.trim();
    }
    if (input.state !== undefined && input.state !== flag.state) {
      changes.push({ field: "state", before: flag.state, after: input.state });
      next.state = input.state;
    }
    if (input.rollout !== undefined) {
      const rollout = validateRollout(input.rollout);
      if (rollout !== flag.rollout) {
        changes.push({ field: "rollout", before: `${flag.rollout}%`, after: `${rollout}%` });
        next.rollout = rollout;
      }
    }
    if (input.environments !== undefined) {
      const environments = input.environments.filter((e) => FLAG_ENVIRONMENTS.includes(e));
      if (environments.length === 0) {
        throw new ApiError("conflict", "A flag has to apply to at least one environment.");
      }
      if ([...environments].sort().join(",") !== [...flag.environments].sort().join(",")) {
        changes.push({
          field: "environments",
          before: flag.environments.join(", "),
          after: environments.join(", "),
        });
        next.environments = environments;
      }
    }
    if (input.ownerId !== undefined && input.ownerId !== flag.ownerId) {
      const owner = db.users.find((u) => u.id === input.ownerId);
      if (!owner) throw new ApiError("conflict", "Choose an owner from the directory.");
      changes.push({ field: "owner", before: flag.ownerId, after: owner.id });
      next.ownerId = owner.id;
    }

    if (changes.length === 0) return flag;
    next.updatedAt = new Date(nowMs()).toISOString();
    next.updatedBy = runtime.actor.id;
    db.flags = db.flags.map((f) => (f.id === id ? next : f));
    appendEvent(db, {
      action: "flag.updated",
      targetType: "flag",
      targetId: next.id,
      targetLabel: next.key,
      changes: diff(changes),
    });
    return next;
  });
}

export async function deleteFlag(id: string): Promise<void> {
  await gate();
  return mutate((db) => {
    const flag = requireFlag(db, id);
    db.flags = db.flags.filter((f) => f.id !== id);
    appendEvent(db, {
      action: "flag.deleted",
      targetType: "flag",
      targetId: flag.id,
      targetLabel: flag.key,
      changes: [{ field: "state", before: flag.state, after: null }],
    });
  });
}
