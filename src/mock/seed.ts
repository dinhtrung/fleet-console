/** Deterministic seed data.
 *
 *  Every list, count and capture in the panel comes from here, so two runs of the same seed
 *  produce the same board. Timestamps are derived from one anchor taken at first run, which is
 *  stored with the data — a reload keeps the board identical, and the recent-activity views stay
 *  meaningful instead of ageing into a fixed date. */

import { DEMO_IDENTITIES } from "./accounts";
import { ROLE_TEMPLATES } from "./permissions";
import { API_SCOPES } from "./types";
import type {
  ApiKey,
  ApiKeyStatus,
  AuditEvent,
  FeatureFlag,
  FieldChange,
  FlagState,
  Membership,
  MembershipRole,
  Organization,
  OrgPlan,
  OrgStatus,
  Role,
  Session,
  User,
  UserStatus,
  WorkspaceSettings,
} from "./types";

export const SCHEMA_VERSION = 4;

export interface Database {
  version: number;
  seededAt: string;
  users: User[];
  roles: Role[];
  organizations: Organization[];
  memberships: Membership[];
  sessions: Session[];
  audit: AuditEvent[];
  apiKeys: ApiKey[];
  flags: FeatureFlag[];
  settings: WorkspaceSettings;
  currentSessionId: string;
}

/** mulberry32 — small, deterministic, and good enough for authored demo data. */
function rng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const FIRST = [
  "Mara", "Tobias", "Ravi", "Ines", "Kelvin", "Yara", "Dmitri", "Amara", "Sofia", "Noor",
  "Hendrik", "Priya", "Lucas", "Zainab", "Mateo", "Elif", "Anders", "Grace", "Idris", "Lena",
  "Tomas", "Ada", "Bruno", "Mei", "Oskar", "Farida", "Jonas", "Camila", "Viktor", "Aisha",
  "Petra", "Sami", "Nadia", "Emil", "Rosa", "Kaito", "Hanna", "Musa", "Clara", "Diego",
];

const LAST = [
  "Ilesanmi", "Brandt", "Krishnan", "Ferreira", "Osei", "Haddad", "Volkov", "Nwosu", "Marchetti",
  "Rahman", "de Vries", "Nair", "Beaumont", "Aziz", "Herrera", "Yilmaz", "Lindqvist", "Adeyemi",
  "Okonkwo", "Novak", "Silva", "Bergström", "Costa", "Zhang", "Kowalski", "Belkacem", "Fischer",
  "Duarte", "Petrov", "Mensah", "Schneider", "Karim", "Haugen", "Vargas", "Rossi", "Tanaka",
];

const ORG_POOL: { name: string; slug: string; plan: OrgPlan; status: OrgStatus }[] = [
  { name: "Aurora Freight", slug: "aurora-freight", plan: "enterprise", status: "active" },
  { name: "Kestrel Health", slug: "kestrel-health", plan: "growth", status: "active" },
  { name: "Meridian Analytics", slug: "meridian-analytics", plan: "growth", status: "active" },
  { name: "Northline Logistics", slug: "northline-logistics", plan: "starter", status: "trial" },
  { name: "Halcyon Ports", slug: "halcyon-ports", plan: "enterprise", status: "active" },
  { name: "Brightwater Co-op", slug: "brightwater-co-op", plan: "starter", status: "past_due" },
  { name: "Verity Public Works", slug: "verity-public-works", plan: "growth", status: "active" },
  { name: "Lantern Union", slug: "lantern-union", plan: "starter", status: "active" },
];

const DEVICES = ["MacBook Pro", "Dell Latitude 7440", "ThinkPad X1 Carbon", "iPhone 15", "Pixel 9", "iPad Air", "Windows Desktop"];
const BROWSERS = ["Chrome 141", "Safari 18", "Firefox 133", "Edge 141"];
const LOCATIONS = [
  "Hanoi, VN", "Ho Chi Minh City, VN", "Singapore, SG", "Helsinki, FI", "Berlin, DE", "Lagos, NG",
  "São Paulo, BR", "Bengaluru, IN", "Toronto, CA", "Warsaw, PL", "Cairo, EG", "Osaka, JP",
];

const HOUR = 3_600_000;
const DAY = 24 * HOUR;

function pick<T>(r: () => number, list: T[]): T {
  return list[Math.floor(r() * list.length)]!;
}

function weightedStatus(r: () => number): UserStatus {
  const n = r();
  if (n < 0.7) return "active";
  if (n < 0.82) return "invited";
  if (n < 0.92) return "suspended";
  return "deactivated";
}

function ip(r: () => number): string {
  return `198.51.${Math.floor(r() * 200) + 10}.${Math.floor(r() * 240) + 5}`;
}

/** The four demo identities exist so every permission state is reachable without an admin
 *  ceremony: the seed always contains one account per system role. */
const DEMO = DEMO_IDENTITIES;

const ACTION_POOL: { action: string; targetType: AuditEvent["targetType"] }[] = [
  { action: "user.invited", targetType: "user" },
  { action: "user.updated", targetType: "user" },
  { action: "user.suspended", targetType: "user" },
  { action: "user.reactivated", targetType: "user" },
  { action: "user.role_changed", targetType: "user" },
  { action: "role.updated", targetType: "role" },
  { action: "role.created", targetType: "role" },
  { action: "organization.member_added", targetType: "organization" },
  { action: "organization.member_removed", targetType: "organization" },
  { action: "organization.updated", targetType: "organization" },
  { action: "session.revoked", targetType: "session" },
  { action: "settings.updated", targetType: "settings" },
];

export function seed(anchorMs: number): Database {
  const r = rng(20261007);
  const iso = (msAgo: number) => new Date(anchorMs - msAgo).toISOString();

  const roles: Role[] = ROLE_TEMPLATES.map((t) => ({
    id: t.id,
    name: t.name,
    description: t.description,
    system: t.system,
    permissions: [...t.permissions],
    createdAt: iso(120 * DAY),
  }));

  const organizations: Organization[] = ORG_POOL.map((o, i) => ({
    id: `org_${String(i + 1).padStart(3, "0")}`,
    name: o.name,
    slug: o.slug,
    plan: o.plan,
    status: o.status,
    createdAt: iso((400 - i * 23) * DAY),
  }));

  const users: User[] = DEMO.map((d, i) => ({
    id: d.id,
    name: d.name,
    email: d.email,
    status: "active",
    roleIds: [d.roleId],
    orgIds: [organizations[i % organizations.length]!.id, organizations[(i + 3) % organizations.length]!.id],
    mfa: i !== 3,
    createdAt: iso((320 - i * 7) * DAY),
    lastActiveAt: iso((0.2 + i * 0.6) * HOUR),
  }));

  const used = new Set(users.map((u) => u.name));
  let seq = 0;
  while (users.length < 148) {
    const name = `${pick(r, FIRST)} ${pick(r, LAST)}`;
    if (used.has(name)) {
      seq += 1;
      if (seq > 400) break;
      continue;
    }
    used.add(name);
    seq += 1;
    const id = `usr_${String(users.length + 1).padStart(3, "0")}`;
    const status = weightedStatus(r);
    const rolePool = ["role_admin", "role_support", "role_viewer", "role_billing", "role_viewer", "role_support"];
    const roleIds = [pick(r, rolePool)];
    if (r() < 0.14) roleIds.push("role_support");
    const orgCount = 1 + Math.floor(r() * 3);
    const orgIds: string[] = [];
    for (let i = 0; i < orgCount; i += 1) {
      const org = pick(r, organizations).id;
      if (!orgIds.includes(org)) orgIds.push(org);
    }
    const email = `${name.toLowerCase().replace(/[^a-z]+/g, ".").replace(/^\.|\.$/g, "")}@panel.example`;
    users.push({
      id,
      name,
      email,
      status,
      roleIds,
      orgIds,
      mfa: r() < 0.62,
      createdAt: iso(Math.floor((5 + r() * 300) * DAY)),
      lastActiveAt: status === "active" && r() < 0.9 ? iso(Math.floor(r() * 14 * DAY)) : status === "invited" ? null : iso(Math.floor((20 + r() * 60) * DAY)),
    });
  }

  const memberships: Membership[] = [];
  for (const org of organizations) {
    const members = users.filter((u) => u.orgIds.includes(org.id));
    if (members.length === 0) continue; // an empty organization is a state the panel must survive
    members.forEach((u, idx) => {
      const memberRole: MembershipRole = idx === 0 ? "owner" : idx === 1 ? "admin" : "member";
      memberships.push({
        orgId: org.id,
        userId: u.id,
        memberRole,
        joinedAt: iso(Math.floor((30 + r() * 250) * DAY)),
      });
    });
  }

  const sessions: Session[] = [];
  const sessionUsers = users.filter((u) => u.status === "active").slice(0, 96);
  for (const u of sessionUsers) {
    const count = 1 + Math.floor(r() * 2);
    for (let i = 0; i < count; i += 1) {
      const started = Math.floor(r() * 9 * DAY);
      sessions.push({
        id: `ses_${String(sessions.length + 1).padStart(4, "0")}`,
        userId: u.id,
        device: pick(r, DEVICES),
        browser: pick(r, BROWSERS),
        location: pick(r, LOCATIONS),
        ip: ip(r),
        startedAt: iso(Math.max(started, 2 * HOUR)),
        lastSeenAt: iso(Math.floor(r() * 6 * HOUR)),
        current: false,
      });
    }
  }

  const audit: AuditEvent[] = [];
  const actors = users.filter((u) => u.status === "active");
  const targetUsers = users.filter((u) => u.status !== "invited" || r() < 0.3);
  for (let i = 0; i < 340; i += 1) {
    const { action, targetType } = pick(r, ACTION_POOL);
    const actor = pick(r, actors);
    let targetId = "";
    let targetLabel = "";
    const changes: FieldChange[] = [];

    if (targetType === "user") {
      const t = pick(r, targetUsers) ?? users[0]!;
      targetId = t.id;
      targetLabel = t.name;
      if (action === "user.updated") {
        changes.push({ field: "name", before: t.name, after: `${pick(r, FIRST)} ${pick(r, LAST)}` });
        changes.push({ field: "mfa", before: t.mfa ? "enabled" : "disabled", after: t.mfa ? "disabled" : "enabled" });
      }
      if (action === "user.suspended" || action === "user.reactivated" || action === "user.invited") {
        const before: UserStatus = action === "user.suspended" ? "active" : action === "user.reactivated" ? "suspended" : "—" as unknown as UserStatus;
        const after: UserStatus = action === "user.suspended" ? "suspended" : action === "user.reactivated" ? "active" : "invited";
        changes.push({ field: "status", before: action === "user.invited" ? null : before, after });
      }
      if (action === "user.role_changed") {
        changes.push({ field: "roles", before: t.roleIds.join(", "), after: pick(r, ["role_support", "role_viewer", "role_admin"]) });
      }
    } else if (targetType === "role") {
      const t = pick(r, roles);
      targetId = t.id;
      targetLabel = t.name;
      changes.push({
        field: "permissions",
        before: `${t.permissions.length} granted`,
        after: `${Math.max(1, t.permissions.length + (r() < 0.5 ? -1 : 1))} granted`,
      });
    } else if (targetType === "organization") {
      const t = pick(r, organizations);
      targetId = t.id;
      targetLabel = t.name;
      if (action === "organization.updated") {
        changes.push({ field: "plan", before: t.plan, after: pick(r, ["starter", "growth", "enterprise"]) });
      } else {
        const u = pick(r, users) ?? users[0]!;
        changes.push({ field: "member", before: null, after: u.name });
      }
    } else if (targetType === "session") {
      const s = pick(r, sessions);
      targetId = s.id;
      targetLabel = `${s.device} · ${s.location}`;
      changes.push({ field: "state", before: "active", after: "revoked" });
    } else {
      targetId = "workspace";
      targetLabel = "Workspace settings";
      changes.push({ field: "name", before: "Admin panel", after: "fleet-console" });
    }

    audit.push({
      id: `evt_${String(i + 1).padStart(4, "0")}`,
      at: iso(Math.floor(r() * 60 * DAY) + Math.floor(r() * 12 * HOUR)),
      actorId: actor.id,
      actorName: actor.name,
      action,
      targetType,
      targetId,
      targetLabel,
      ip: ip(r),
      changes,
    });
  }
  audit.sort((a, b) => (a.at < b.at ? 1 : -1));

  /* ---- API keys: the dialog CRUD surface. The secret is never seeded or stored — only a
   *      fingerprint and the last four characters, so nothing here can be replayed. ---- */
  const API_KEY_NAMES = [
    "CI pipeline",
    "Analytics export",
    "Mobile app",
    "Support tooling",
    "Billing sync",
    "Data warehouse load",
    "Webhook relay",
    "Status page",
    "On-call rotation",
    "Partner integration",
    "Reconciliation job",
    "Audit exporter",
    "Kiosk device",
    "Legacy importer",
    "Load test harness",
    "Regional failover",
  ];

  const apiKeys: ApiKey[] = API_KEY_NAMES.map((name, i) => {
    const status: ApiKeyStatus = i % 6 === 4 ? "revoked" : "active";
    const scopeCount = 1 + Math.floor(r() * 3);
    const scopes: string[] = [];
    for (let s = 0; s < scopeCount; s += 1) {
      const scope = pick(r, API_SCOPES).id;
      if (!scopes.includes(scope)) scopes.push(scope);
    }
    const createdAt = iso(Math.floor((3 + r() * 320) * DAY));
    return {
      id: `key_${String(i + 1).padStart(3, "0")}`,
      name,
      fingerprint: `fp_${Math.floor(r() * 1e12).toString(36)}${Math.floor(r() * 1e6).toString(36)}`,
      lastFour: Math.floor(r() * 10000)
        .toString()
        .padStart(4, "0"),
      scopes,
      environment: r() < 0.72 ? "live" : "test",
      status,
      createdAt,
      lastUsedAt: status === "revoked" || r() < 0.18 ? null : iso(Math.floor(r() * 18 * DAY)),
      createdBy: pick(r, DEMO).id,
    };
  });

  /* ---- Feature flags: the side-panel CRUD surface ---- */
  const FLAG_SEED: {
    key: string;
    name: string;
    description: string;
    state: FlagState;
    rollout: number;
  }[] = [
    {
      key: "checkout.express-lane",
      name: "Express lane at checkout",
      description: "Skip address validation for customers who have ordered before.",
      state: "gradual",
      rollout: 25,
    },
    {
      key: "search.v2-ranking",
      name: "Ranking model v2",
      description: "Second-generation ranking behind the same search surface.",
      state: "gradual",
      rollout: 60,
    },
    {
      key: "notifications.digest-email",
      name: "Daily digest email",
      description: "One summary message per day instead of one per event.",
      state: "on",
      rollout: 100,
    },
    {
      key: "billing.invoice-pdf",
      name: "Invoice PDF generation",
      description: "Generate the invoice document on demand rather than at issue time.",
      state: "on",
      rollout: 100,
    },
    {
      key: "onboarding.checklist",
      name: "Onboarding checklist",
      description: "Guide a new workspace through its first five tasks.",
      state: "on",
      rollout: 100,
    },
    {
      key: "sessions.geo-alerts",
      name: "Impossible-travel alerts",
      description: "Flag a session whose location cannot follow the previous one.",
      state: "off",
      rollout: 0,
    },
    {
      key: "audit.streaming-export",
      name: "Streaming audit export",
      description: "Ship audit events to the warehouse continuously rather than nightly.",
      state: "off",
      rollout: 0,
    },
    {
      key: "audit.retention-extension",
      name: "Extended audit retention",
      description: "Keep the audit record for 24 months instead of 12.",
      state: "gradual",
      rollout: 10,
    },
    {
      key: "roles.matrix-quickedit",
      name: "Quick edit in the permission matrix",
      description: "Stage and apply permission changes without leaving the page.",
      state: "on",
      rollout: 100,
    },
    {
      key: "tables.column-presets",
      name: "Saved column presets",
      description: "Remember a column selection per list and per operator.",
      state: "gradual",
      rollout: 40,
    },
    {
      key: "tables.density-compact",
      name: "Compact row density",
      description: "Offer a tighter row height for very long lists.",
      state: "off",
      rollout: 0,
    },
    {
      key: "orgs.scope-switcher-v2",
      name: "Scope switcher v2",
      description: "Switch the board's organization scope without a full reload.",
      state: "on",
      rollout: 100,
    },
    {
      key: "security.step-up-reauth",
      name: "Step-up re-authentication",
      description: "Ask for a fresh credential before a destructive action.",
      state: "off",
      rollout: 0,
    },
    {
      key: "security.session-cap",
      name: "Session count cap",
      description: "Refuse a new session beyond the operator's device limit.",
      state: "gradual",
      rollout: 15,
    },
    {
      key: "support.context-panel",
      name: "Support context panel",
      description: "Show the last ten events for a record beside the record.",
      state: "on",
      rollout: 100,
    },
    {
      key: "search.saved-queries",
      name: "Saved queries",
      description: "Keep a named filter set per operator.",
      state: "off",
      rollout: 0,
    },
    {
      key: "runtime.mock-latency",
      name: "Simulated request latency",
      description: "Keep the demo's latency so the loading states are visible.",
      state: "on",
      rollout: 100,
    },
    {
      key: "runtime.failure-switch",
      name: "Failure switch",
      description: "Expose the panel's failure injection to whoever is reviewing it.",
      state: "on",
      rollout: 100,
    },
  ];

  const flagOwners = users.filter((u) => u.status === "active" && (u.roleIds.includes("role_admin") || u.roleIds.includes("role_owner")));
  const environmentSets = [
    ["production", "staging"],
    ["production"],
    ["staging", "development"],
    ["production", "staging", "development"],
  ];

  const flags: FeatureFlag[] = FLAG_SEED.map((f, i) => {
    const createdAt = iso(Math.floor((20 + r() * 260) * DAY));
    const owner = pick(r, flagOwners.length > 0 ? flagOwners : users);
    return {
      id: `flag_${String(i + 1).padStart(3, "0")}`,
      key: f.key,
      name: f.name,
      description: f.description,
      state: f.state,
      rollout: f.rollout,
      environments: pick(r, environmentSets),
      ownerId: owner.id,
      createdAt,
      updatedAt: iso(Math.floor(r() * 21 * DAY)),
      updatedBy: pick(r, flagOwners.length > 0 ? flagOwners : users).id,
    };
  });

  /* ---- a short history for the two new surfaces, so the audit record is not silent about them ---- */
  const history: AuditEvent[] = [];
  const pushHistory = (
    action: string,
    targetType: AuditEvent["targetType"],
    targetId: string,
    targetLabel: string,
    changes: FieldChange[],
  ) => {
    const actor = pick(r, actors);
    history.push({
      id: `evt_${String(9000 + history.length).padStart(4, "0")}`,
      at: iso(Math.floor(r() * 45 * DAY) + Math.floor(r() * 12 * HOUR)),
      actorId: actor.id,
      actorName: actor.name,
      action,
      targetType,
      targetId,
      targetLabel,
      ip: ip(r),
      changes,
    });
  };

  for (const key of apiKeys.slice(0, 9)) {
    pushHistory("apikey.issued", "api_key", key.id, key.name, [
      { field: "scopes", before: null, after: key.scopes.join(", ") },
      { field: "environment", before: null, after: key.environment },
    ]);
    if (key.status === "revoked") {
      pushHistory("apikey.revoked", "api_key", key.id, key.name, [
        { field: "status", before: "active", after: "revoked" },
      ]);
    } else if (r() < 0.4) {
      pushHistory("apikey.updated", "api_key", key.id, key.name, [
        { field: "scopes", before: `${key.scopes.length} granted`, after: `${key.scopes.length + 1} granted` },
      ]);
    }
  }
  for (const flag of flags.slice(0, 10)) {
    pushHistory("flag.created", "flag", flag.id, flag.key, [
      { field: "state", before: null, after: flag.state },
    ]);
    if (r() < 0.5) {
      pushHistory("flag.updated", "flag", flag.id, flag.key, [
        { field: "rollout", before: String(Math.max(0, flag.rollout - 15)), after: `${flag.rollout}%` },
      ]);
    }
  }
  audit.push(...history);
  audit.sort((a, b) => (a.at < b.at ? 1 : -1));

  const settings: WorkspaceSettings = {
    name: "Admin panel",
    slug: "fleet-console",
    defaultUserStatus: "invited",
    defaultLanding: "dashboard",
  };

  return {
    version: SCHEMA_VERSION,
    seededAt: new Date(anchorMs).toISOString(),
    users,
    roles,
    organizations,
    memberships,
    sessions,
    audit,
    apiKeys,
    flags,
    settings,
    currentSessionId: sessions[0]?.id ?? "ses_0001",
  };
}
