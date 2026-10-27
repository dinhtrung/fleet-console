/** Access control: one role→permission model, one way to ask.
 *
 *  A route that the identity may not open renders a denied surface rather than an empty screen, and
 *  an action the identity may not perform is never offered — the interface does not show controls
 *  that can only fail. */

import type { ReactNode } from "react";
import { useAuth } from "../auth/session";

export function useAccess() {
  const { permissions, identity } = useAuth();
  return {
    identity,
    permissions,
    can: (permission: string) => permissions.includes(permission),
    canAny: (required: string[]) => required.some((p) => permissions.includes(p)),
    canAll: (required: string[]) => required.every((p) => permissions.includes(p)),
  };
}

/** Renders its children only when the permission is granted. `fallback` is for the rare case where
 *  something must be shown in place of the action (an explanation, never a disabled button). */
export function Gate({
  permission,
  children,
  fallback = null,
}: {
  permission: string;
  children: ReactNode;
  fallback?: ReactNode;
}) {
  const { can } = useAccess();
  return <>{can(permission) ? children : fallback}</>;
}
