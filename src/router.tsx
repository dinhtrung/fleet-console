/** The route tree.
 *
 *  Code-based routes rather than a generated tree: the addresses are part of the frozen contract
 *  (deep links must resolve), so they are declared in one file. The root route owns the shell; each
 *  screen owns its own header and permission gate. */

import { createRootRoute, createRoute, createRouter, Outlet, useNavigate } from "@tanstack/react-router";
import { AppShell } from "./shell/AppShell";
import { BoardScreen } from "./screens/BoardScreen";
import { DecisionsScreen } from "./screens/DecisionsScreen";
import { SeatsScreen } from "./screens/SeatsScreen";
import { ProjectsScreen } from "./screens/ProjectsScreen";
import { CultureScreen } from "./screens/CultureScreen";
import { TaskDrawerScreen } from "./screens/TaskDrawerScreen";
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
import { Button, ErrorState, NotFoundState } from "./components/ui";

/** The shell wraps every screen. The default landing surface used to be read from a workspace
 *  setting; the real configuration has no such key, so the board opens on its own overview and a
 *  deep link is never redirected. */
function RootLayout() {
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

const dashboardRoute = createRoute({ getParentRoute: () => rootRoute, path: "/", component: BoardScreen });

const projectsRoute = createRoute({ getParentRoute: () => rootRoute, path: "/projects", component: ProjectsScreen });

const cultureRoute = createRoute({ getParentRoute: () => rootRoute, path: "/culture", component: CultureScreen });

const decisionsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/decisions",
  component: DecisionsScreen,
});

const seatsRoute = createRoute({ getParentRoute: () => rootRoute, path: "/seats", component: SeatsScreen });

const taskDrawerRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/items/$number",
  component: TaskDrawerScreen,
});

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
  projectsRoute,
  cultureRoute,
  decisionsRoute,
  seatsRoute,
  taskDrawerRoute,
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
