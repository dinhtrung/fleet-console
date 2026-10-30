/** The four demo identities.
 *
 *  The board has one operator account (`usr_owner`, every permission); the other three exist so each
 *  system role has a holder and the people screens have something real to show. They are authored
 *  synthetic people; nothing here refers to a real person. */

import type { User } from "./types";

export interface DemoIdentity {
  id: string;
  name: string;
  email: string;
  roleId: string;
  note: string;
}

export const DEMO_IDENTITIES: DemoIdentity[] = [
  {
    id: "usr_owner",
    name: "Operator",
    email: "operator@fleet-console.local",
    roleId: "role_owner",
    note: "The local operator — every permission, including workspace settings",
  },
  {
    id: "usr_admin",
    name: "Tobias Brandt",
    email: "tobias.brandt@panel.example",
    roleId: "role_admin",
    note: "Administrator — everything except workspace settings",
  },
  {
    id: "usr_support",
    name: "Priya Nair",
    email: "priya.nair@panel.example",
    roleId: "role_support",
    note: "Support — reads the board and can end sessions",
  },
  {
    id: "usr_viewer",
    name: "Grace Adeyemi",
    email: "grace.adeyemi@panel.example",
    roleId: "role_viewer",
    note: "Viewer — read-only",
  },
];

/** A fallback board containing only the demo accounts, used before the store has ever been written
 *  (the session can then resolve the operator even if storage is unavailable). */
export const DEMO_ACCOUNTS: User[] = DEMO_IDENTITIES.map((d, i) => ({
  id: d.id,
  name: d.name,
  email: d.email,
  status: "active" as const,
  roleIds: [d.roleId],
  orgIds: [],
  mfa: i !== 3,
  createdAt: "2025-11-20T09:00:00.000Z",
  lastActiveAt: "2026-10-06T08:00:00.000Z",
}));
