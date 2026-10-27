/** The permission catalogue and the role templates the seed starts from.
 *
 *  Permissions are grouped so the role editor can render a matrix rather than a flat list, and
 *  every route and every destructive action in the panel resolves through one of these ids. */

export interface PermissionDef {
  id: string;
  group: string;
  label: string;
  /** Destructive actions are confirmed before they run and are never granted with read access. */
  destructive?: boolean;
}

export const PERMISSIONS: PermissionDef[] = [
  { id: "dashboard.read", group: "Overview", label: "Read the overview" },

  { id: "users.read", group: "People", label: "Read the user directory" },
  { id: "users.invite", group: "People", label: "Invite users" },
  { id: "users.write", group: "People", label: "Edit users" },
  { id: "users.deactivate", group: "People", label: "Suspend and deactivate users", destructive: true },

  { id: "roles.read", group: "Access", label: "Read roles" },
  { id: "roles.write", group: "Access", label: "Create and edit roles", destructive: true },
  { id: "orgs.read", group: "Access", label: "Read organizations" },
  { id: "orgs.write", group: "Access", label: "Manage organizations and members" },

  { id: "sessions.read", group: "Security", label: "Read active sessions" },
  { id: "sessions.revoke", group: "Security", label: "Revoke sessions", destructive: true },
  { id: "audit.read", group: "Security", label: "Read the audit record" },

  { id: "apikeys.read", group: "Credentials", label: "Read API keys" },
  { id: "apikeys.write", group: "Credentials", label: "Issue, edit and revoke API keys", destructive: true },
  { id: "flags.read", group: "Delivery", label: "Read feature flags" },
  { id: "flags.write", group: "Delivery", label: "Create, edit and delete feature flags" },

  { id: "settings.write", group: "Workspace", label: "Change workspace settings", destructive: true },
];

export const PERMISSION_GROUPS: string[] = [...new Set(PERMISSIONS.map((p) => p.group))];

export const PERMISSION_IDS: string[] = PERMISSIONS.map((p) => p.id);

export function permissionLabel(id: string): string {
  return PERMISSIONS.find((p) => p.id === id)?.label ?? id;
}

const ALL = PERMISSION_IDS;

export interface RoleTemplate {
  id: string;
  name: string;
  description: string;
  system: boolean;
  permissions: string[];
}

/** Four system roles. `owner` is the only one that can change the workspace, and it is the role the
 *  last-administration rule protects: the panel refuses to leave the workspace without one. */
export const ROLE_TEMPLATES: RoleTemplate[] = [
  {
    id: "role_owner",
    name: "Owner",
    description: "Full control, including workspace settings and destructive operations.",
    system: true,
    permissions: ALL,
  },
  {
    id: "role_admin",
    name: "Administrator",
    description: "Everything except changing the workspace itself.",
    system: true,
    permissions: ALL.filter((p) => p !== "settings.write"),
  },
  {
    id: "role_support",
    name: "Support",
    description: "Reads the directory and can end sessions, but never changes people or access.",
    system: true,
    permissions: [
      "dashboard.read",
      "users.read",
      "sessions.read",
      "sessions.revoke",
      "audit.read",
      "orgs.read",
      "apikeys.read",
      "flags.read",
    ],
  },
  {
    id: "role_viewer",
    name: "Viewer",
    description: "Read-only across the board.",
    system: true,
    permissions: [
      "dashboard.read",
      "users.read",
      "roles.read",
      "orgs.read",
      "sessions.read",
      "audit.read",
      "apikeys.read",
      "flags.read",
    ],
  },
  {
    id: "role_billing",
    name: "Billing contact",
    description: "Custom role: sees organizations and the audit record, and nothing else.",
    system: false,
    permissions: ["dashboard.read", "orgs.read", "audit.read"],
  },
];

export function permissionUnion(roleIds: string[], roles: { id: string; permissions: string[] }[]): string[] {
  const set = new Set<string>();
  for (const role of roles) {
    if (!roleIds.includes(role.id)) continue;
    for (const p of role.permissions) set.add(p);
  }
  return [...set];
}

export function hasPermission(granted: string[], required: string): boolean {
  return granted.includes(required);
}

/** The role that keeps the workspace administrable, whichever one carries it. */
export function isAdministrationRole(permissions: string[]): boolean {
  return permissions.includes("users.deactivate") && permissions.includes("roles.write");
}
