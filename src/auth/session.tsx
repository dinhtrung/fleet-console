/** The operator session.
 *
 *  fleet-console serves one operator behind a loopback address: there is no identity provider and
 *  no sign-in step, so the session is the local operator account, resolved during the first render
 *  with every permission its roles grant. Nothing is stored and nothing expires — an idle tab must
 *  not ask for a ceremony the board has no way to complete.
 *
 *  `switchTo` is the one survivor of the old sign-in flow: the settings form uses it to re-apply
 *  the identity after it renames the operator, so the rail shows the new name without a reload. */

import { createContext, useCallback, useContext, useEffect, useState } from "react";
import type { ReactNode } from "react";
import { bindCurrentSession, listRoles, setActor } from "../mock/api";
import { permissionUnion, ROLE_TEMPLATES } from "../mock/permissions";
import { DEMO_ACCOUNTS } from "../mock/accounts";
import type { Identity, User } from "../mock/types";

/** The seeded owner: every permission, including the workspace settings. */
const OPERATOR_ID = "usr_owner";

export interface AuthContextValue {
  identity: Identity;
  permissions: string[];
  /** Re-apply an account after its own record changed (a rename in settings). */
  switchTo: (userId: string) => Promise<Identity>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

function storeUsers(): User[] {
  try {
    const raw = localStorage.getItem("fleet-console.db");
    if (raw) {
      const parsed = JSON.parse(raw) as { users?: User[] };
      if (parsed.users && parsed.users.length > 0) return parsed.users;
    }
  } catch {
    // fall through to the seeded accounts
  }
  return DEMO_ACCOUNTS;
}

function identityOf(user: User): Identity {
  return { userId: user.id, name: user.name, email: user.email, roleIds: user.roleIds };
}

function operatorIdentity(): Identity {
  const users = storeUsers();
  const user = users.find((u) => u.id === OPERATOR_ID) ?? users[0] ?? DEMO_ACCOUNTS[0]!;
  return identityOf(user);
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [identity, setIdentity] = useState<Identity>(operatorIdentity);
  // The first paint resolves permissions from the role templates (synchronous, no gate); the store
  // is read right after so an edited role still governs which surfaces the operator can open.
  const [permissions, setPermissions] = useState<string[]>(() =>
    permissionUnion(identity.roleIds, ROLE_TEMPLATES),
  );

  useEffect(() => {
    setActor(identity);
    void bindCurrentSession(identity.userId);
    let live = true;
    void listRoles()
      .then((roles) => {
        if (live) setPermissions(permissionUnion(identity.roleIds, roles));
      })
      .catch(() => {
        // A store that cannot be read keeps the template permissions rather than emptying the board.
      });
    return () => {
      live = false;
    };
  }, [identity]);

  const switchTo = useCallback(async (userId: string): Promise<Identity> => {
    const user = storeUsers().find((u) => u.id === userId);
    if (!user) throw new Error(`Unknown account ${userId}.`);
    const next = identityOf(user);
    setIdentity(next);
    return next;
  }, []);

  return (
    <AuthContext.Provider value={{ identity, permissions, switchTo }}>{children}</AuthContext.Provider>
  );
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used inside AuthProvider");
  return ctx;
}
