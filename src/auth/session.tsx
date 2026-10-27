/** Mock authentication.
 *
 *  There is no identity provider: an account is one of the seeded users, any non-empty password
 *  is accepted, and the session lives in browser storage. What is real is the behaviour around it —
 *  status is enforced at sign-in, the session survives a reload, it expires after idle time with an
 *  explicit re-authentication path, and a protected location is remembered and returned to. */

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { bindCurrentSession, setActor, listRoles } from "../mock/api";
import { permissionUnion } from "../mock/permissions";
import type { Identity } from "../mock/types";

const IDENTITY_KEY = "fleet-console.identity";
const LAST_SEEN_KEY = "fleet-console.lastSeen";
export const DEFAULT_IDLE_MS = 30 * 60 * 1000;

export type SessionState = "restoring" | "anonymous" | "signed_in" | "expired";

export interface AuthContextValue {
  identity: Identity | null;
  state: SessionState;
  idleMs: number;
  permissions: string[];
  signIn: (email: string, password: string) => Promise<Identity>;
  signOut: (reason?: "user" | "revoked" | "expired") => void;
  touch: () => void;
  /** Demo affordance: swap the acting account without a password. */
  switchTo: (userId: string) => Promise<Identity>;
  findAccount: (email: string) => { userId: string; name: string; email: string } | null;
}

const AuthContext = createContext<AuthContextValue | null>(null);

function idleMsFromLocation(): number {
  if (typeof window === "undefined") return DEFAULT_IDLE_MS;
  const raw = new URLSearchParams(window.location.search).get("idle");
  const seconds = raw ? Number.parseInt(raw, 10) : Number.NaN;
  return Number.isFinite(seconds) && seconds > 0 ? seconds * 1000 : DEFAULT_IDLE_MS;
}

/** The session is resolved during the first render, not in an effect: an effect would paint the
 *  sign-in screen for a frame on every reload by an operator who is already signed in. */
function readStoredSession(idleMs: number): { state: SessionState; identity: Identity | null } {
  if (typeof window === "undefined") return { state: "anonymous", identity: null };
  try {
    const raw = localStorage.getItem(IDENTITY_KEY);
    if (!raw) return { state: "anonymous", identity: null };
    const seenAt = Number(localStorage.getItem(LAST_SEEN_KEY) ?? Date.now());
    if (Date.now() - seenAt > idleMs) return { state: "expired", identity: null };
    return { state: "restoring", identity: JSON.parse(raw) as Identity };
  } catch {
    // A corrupt record is not a session: drop it and start clean rather than trusting the parse.
    localStorage.removeItem(IDENTITY_KEY);
    return { state: "anonymous", identity: null };
  }
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const idleMs = useMemo(() => idleMsFromLocation(), []);
  const [restored] = useState(() => readStoredSession(idleMs));
  const [identity, setIdentity] = useState<Identity | null>(null);
  const [state, setState] = useState<SessionState>(restored.state);
  const [permissions, setPermissions] = useState<string[]>([]);
  const lastSeen = useRef<number>(0);

  const applyIdentity = useCallback(async (next: Identity) => {
    const roles = await listRoles();
    setIdentity(next);
    setPermissions(permissionUnion(next.roleIds, roles));
    setActor(next);
    localStorage.setItem(IDENTITY_KEY, JSON.stringify(next));
    localStorage.setItem(LAST_SEEN_KEY, String(Date.now()));
    lastSeen.current = Date.now();
    setState("signed_in");
    await bindCurrentSession(next.userId);
  }, []);

  // One restore path: while `restoring`, the identity parsed during the first render is validated.
  // The async load settles into `signed_in`; nothing is set synchronously here.
  // (oxlint's set-state-in-effect heuristic flags the call: `applyIdentity` is async and awaits
  // `listRoles()` before any state update, so the update is not synchronous.)
  useEffect(() => {
    if (state !== "restoring" || !restored.identity) return;
    void applyIdentity(restored.identity);
  }, [state, restored, applyIdentity]);

  const touch = useCallback(() => {
    lastSeen.current = Date.now();
    localStorage.setItem(LAST_SEEN_KEY, String(lastSeen.current));
  }, []);

  // Activity keeps the session alive; idleness ends it, and the panel says so rather than
  // silently dropping the operator on a page that no longer works.
  useEffect(() => {
    if (state !== "signed_in") return;
    const onActivity = () => touch();
    const events: (keyof WindowEventMap)[] = ["pointerdown", "keydown", "focus"];
    events.forEach((e) => window.addEventListener(e, onActivity));
    const timer = window.setInterval(() => {
      const reference = lastSeen.current || Date.now();
      if (Date.now() - reference > idleMs) {
        setState("expired");
        setActor(null);
        localStorage.removeItem(IDENTITY_KEY);
      }
    }, Math.min(15_000, Math.max(2_000, Math.floor(idleMs / 10))));
    return () => {
      events.forEach((e) => window.removeEventListener(e, onActivity));
      window.clearInterval(timer);
    };
  }, [idleMs, state, touch]);

  const signIn = useCallback(
    async (email: string, password: string): Promise<Identity> => {
      const account = findAccountByEmail(email);
      if (!account) throw new Error(`No account on this board uses ${email}.`);
      if (password.trim().length === 0) throw new Error("Enter a password to continue.");
      if (account.status !== "active") {
        throw new Error(
          account.status === "invited"
            ? `${account.name} has not accepted the invitation yet.`
            : `${account.name} is ${account.status} and cannot sign in.`,
        );
      }
      await applyIdentity(account.identity);
      return account.identity;
    },
    [applyIdentity],
  );

  const switchTo = useCallback(
    async (userId: string): Promise<Identity> => {
      const account = findAccountById(userId);
      if (!account) throw new Error(`Unknown account ${userId}.`);
      await applyIdentity(account.identity);
      return account.identity;
    },
    [applyIdentity],
  );

  const signOut = useCallback(
    (reason: "user" | "revoked" | "expired" = "user") => {
      localStorage.removeItem(IDENTITY_KEY);
      localStorage.removeItem(LAST_SEEN_KEY);
      setActor(null);
      setIdentity(null);
      setPermissions([]);
      setState(reason === "expired" ? "expired" : "anonymous");
    },
    [],
  );

  const value: AuthContextValue = {
    identity,
    state,
    idleMs,
    permissions,
    signIn,
    signOut,
    touch,
    switchTo,
    findAccount: (email) => {
      const account = findAccountByEmail(email);
      return account ? { userId: account.identity.userId, name: account.identity.name, email: account.identity.email } : null;
    },
  };

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used inside AuthProvider");
  return ctx;
}

/* ------------------------------------------------------------------ account lookup
 * The seeded accounts are read straight from the store; the sign-in screen lists them so every
 * permission state is reachable without an administrator ceremony. */

import { DEMO_ACCOUNTS } from "../mock/accounts";
import type { User } from "../mock/types";

interface Account {
  identity: Identity;
  name: string;
  email: string;
  status: User["status"];
}

function accountFrom(user: User): Account {
  return {
    identity: { userId: user.id, name: user.name, email: user.email, roleIds: user.roleIds },
    name: user.name,
    email: user.email,
    status: user.status,
  };
}

function findAccountByEmail(email: string): Account | null {
  const target = email.trim().toLowerCase();
  const user = demoUsers().find((u) => u.email.toLowerCase() === target);
  return user ? accountFrom(user) : null;
}

function findAccountById(userId: string): Account | null {
  const user = demoUsers().find((u) => u.id === userId);
  return user ? accountFrom(user) : null;
}

function demoUsers(): User[] {
  try {
    const raw = localStorage.getItem("fleet-console.db");
    if (raw) {
      const parsed = JSON.parse(raw) as { users?: User[] };
      if (parsed.users && parsed.users.length > 0) return parsed.users;
    }
  } catch {
    // fall through to the advertised demo accounts below
  }
  return DEMO_ACCOUNTS;
}
