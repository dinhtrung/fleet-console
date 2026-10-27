/** The route tree.
 *
 *  Code-based routes rather than a generated tree: the addresses are part of the frozen contract
 *  (deep links must resolve), so they are declared in one file. The root route owns the auth gate and
 *  the shell; each screen owns its own header and permission gate. */

import { useEffect, useRef } from "react";
import { createRootRoute, createRoute, createRouter, Outlet, useNavigate, useRouterState } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { AppShell } from "./shell/AppShell";
import { useAuth } from "./auth/session";
import { getSettings } from "./mock/api";
import type { WorkspaceSettings } from "./mock/types";
import { SignInScreen } from "./screens/SignInScreen";
import { DashboardScreen } from "./screens/DashboardScreen";
import { UserListScreen } from "./screens/UserListScreen";
import { UserDetailScreen } from "./screens/UserDetailScreen";
import { RoleListScreen } from "./screens/RoleListScreen";
import { RoleDetailScreen } from "./screens/RoleDetailScreen";
import { OrganizationListScreen } from "./screens/OrganizationListScreen";
import { OrganizationDetailScreen } from "./screens/OrganizationDetailScreen";
import { SessionListScreen } from "./screens/SessionListScreen";
import { AuditListScreen } from "./screens/AuditListScreen";
import { AuditDetailScreen } from "./screens/AuditDetailScreen";
import { SettingsScreen } from "./screens/SettingsScreen";
import { ApiKeysScreen } from "./screens/ApiKeysScreen";
import { FeatureFlagsScreen } from "./screens/FeatureFlagsScreen";
import { Button, ErrorState, LoadingRows, NotFoundState } from "./components/ui";

/** The saved default landing surface, as an address. */
const LANDING: Record<WorkspaceSettings["defaultLanding"], string> = {
  dashboard: "/",
  users: "/users",
  sessions: "/sessions",
  audit: "/audit",
};

function RootLayout() {
  const { state } = useAuth();
  const navigate = useNavigate();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const applied = useRef(false);
  const { data: settings } = useQuery({
    queryKey: ["settings"],
    queryFn: getSettings,
    enabled: state === "signed_in",
  });

  // The saved default landing surface is honoured once per sign-in, and only when the operator
  // arrived on the landing surface itself: an explicit deep link is never hijacked.
  useEffect(() => {
    if (state !== "signed_in") {
      applied.current = false;
      return;
    }
    if (applied.current || !settings) return;
    applied.current = true;
    if (pathname !== "/") return;
    const target = LANDING[settings.defaultLanding] ?? "/";
    if (target !== "/") void navigate({ to: target });
  }, [state, settings, pathname, navigate]);

  if (state === "restoring") {
    return (
      <div className="min-h-svh bg-ground px-3 py-4 sm:px-5">
        <p className="label text-ink-muted">Restoring your session</p>
        <div className="mt-3 border border-rule bg-panel">
          <LoadingRows rows={6} />
        </div>
      </div>
    );
  }
  if (state !== "signed_in") return <SignInScreen expired={state === "expired"} />;
  return (
    <AppShell>
      <Outlet />
    </AppShell>
  );
}

function RouteNotFound() {
  const navigate = useNavigate();
  return (
    <div className="px-3 sm:px-5">
      <NotFoundState what="page">
        <Button variant="outline" size="sm" onClick={() => void navigate({ to: "/" })}>
          Back to the overview
        </Button>
      </NotFoundState>
    </div>
  );
}

const rootRoute = createRootRoute({
  component: RootLayout,
  errorComponent: (props) => (
    <div className="px-3 sm:px-5">
      <ErrorState title="This surface did not load" error={props.error} onRetry={() => props.reset()} />
    </div>
  ),
  notFoundComponent: RouteNotFound,
});

const dashboardRoute = createRoute({ getParentRoute: () => rootRoute, path: "/", component: DashboardScreen });

const usersRoute = createRoute({ getParentRoute: () => rootRoute, path: "/users", component: UserListScreen });

const userDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/users/$userId",
  component: function UserDetailRoute() {
    const { userId } = userDetailRoute.useParams();
    return <UserDetailScreen userId={userId} />;
  },
});

const rolesRoute = createRoute({ getParentRoute: () => rootRoute, path: "/roles", component: RoleListScreen });

const roleDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/roles/$roleId",
  component: function RoleDetailRoute() {
    const { roleId } = roleDetailRoute.useParams();
    return <RoleDetailScreen roleId={roleId} />;
  },
});

const organizationsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/organizations",
  component: OrganizationListScreen,
});

const organizationDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/organizations/$orgId",
  component: function OrganizationDetailRoute() {
    const { orgId } = organizationDetailRoute.useParams();
    return <OrganizationDetailScreen orgId={orgId} />;
  },
});

const sessionsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/sessions",
  component: SessionListScreen,
});

const auditRoute = createRoute({ getParentRoute: () => rootRoute, path: "/audit", component: AuditListScreen });

const auditDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/audit/$eventId",
  component: function AuditDetailRoute() {
    const { eventId } = auditDetailRoute.useParams();
    return <AuditDetailScreen eventId={eventId} />;
  },
});

const settingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/settings",
  component: SettingsScreen,
});

const apiKeysRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/api-keys",
  component: ApiKeysScreen,
});

const featureFlagsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/feature-flags",
  component: FeatureFlagsScreen,
});

const routeTree = rootRoute.addChildren([
  dashboardRoute,
  usersRoute,
  userDetailRoute,
  rolesRoute,
  roleDetailRoute,
  organizationsRoute,
  organizationDetailRoute,
  sessionsRoute,
  auditRoute,
  auditDetailRoute,
  settingsRoute,
  apiKeysRoute,
  featureFlagsRoute,
]);

export const router = createRouter({ routeTree, defaultPreload: false });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
