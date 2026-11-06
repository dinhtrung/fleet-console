/** The mock API layer: what the identity scaffold still needs.
 *
 *  Every shipped screen reads the real API; this module survives for one reason — the app has one
 *  token and no user row, so the shell's `useAuth()` reads its identities from here. Replacing that
 *  means deciding what identity a loopback console has, which is its own change (design D2).
 *
 *  It owns the seeded store and the runtime controls the board's dev panel uses. The screen-shaped
 *  functions — users, roles, organizations, sessions, api keys, flags — were removed with the
 *  screens that read them; `scripts/check-no-mock-screens.mjs` keeps that true. */

import { SCHEMA_VERSION, seed, type Database } from "./seed";
import {
  ApiError,
  type Identity,
  type ListQuery,
  type Organization,
  type Page,
  type Role,
  type Session,
} from "./types";

export const PAGE_SIZES = [10, 25, 50] as const;
export const DEFAULT_PAGE_SIZE = 25;

const STORAGE_KEY = "fleet-console.db";
const FAILURE_KEY = "fleet-console.failure";

export type FailureMode = "off" | "next" | "always";

interface Runtime {
  latencyMs: number;
  failure: FailureMode;
  actor: { id: string; name: string };
}

function storedFailure(): FailureMode {
  try {
    const raw = sessionStorage.getItem(FAILURE_KEY);
    if (raw === "next" || raw === "always" || raw === "off") return raw;
  } catch {
    // storage unavailable: default to no failure
  }
  return "off";
}

const runtime: Runtime = {
  latencyMs: 120,
  failure: storedFailure(),
  actor: { id: "system", name: "System" },
};

function nowMs(): number {
  return Date.now();
}

let store: Database | null = null;

function readStore(): Database {
  if (store) return store;
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as Database;
      if (parsed.version === SCHEMA_VERSION) {
        store = parsed;
        return store;
      }
    }
  } catch {
    // storage unavailable or corrupt: fall through to a fresh seed
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
    // storage full or unavailable: the in-memory store still serves this session
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

/* ------------------------------------------------------------------ reads */

export async function listRoles(): Promise<(Role & { memberCount: number })[]> {
  await gate();
  const db = readStore();
  return db.roles.map((role) => ({
    ...role,
    memberCount: db.users.filter((u) => u.roleIds.includes(role.id)).length,
  }));
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

/* ------------------------------------------------------------------ gate */

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
