/** The landing surface: what state is the board in, right now.
 *
 *  Read at a glance and then acted on: one ruled tally strip (not four KPI cards) carries the
 *  headline counts with their change against the period before, the recent-activity list is a
 *  column of links straight into the audit detail, and the quick actions name the permission each
 *  one needs instead of failing when it is pressed. Everything is scoped to the organization the
 *  shell has selected and recomputes the moment that selection changes. */

import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import type { LucideIcon } from "lucide-react";
import {
  ArrowRight,
  ArrowUpRight,
  Building,
  MonitorSmartphone,
  ScrollText,
  ShieldCheck,
  UserPlus,
  Users,
} from "lucide-react";
import { dashboardSummary, listOrganizations, listSessions, listUsers } from "../mock/api";
import type { AuditEvent, DashboardSummary, Page } from "../mock/types";
import { Delta, EmptyState, ErrorState, LoadingRows, Panel, PanelHead } from "../components/ui";
import { RequirePermission } from "../components/RequirePermission";
import { PageHeader } from "../shell/PageHeader";
import { SeedNotice } from "../shell/BoardControls";
import { useScope } from "../shell/scope";
import { Gate, useAccess } from "../access/access";
import { cn } from "../lib/cn";
import { dateTime, number, plural, relativeTime } from "../lib/format";

/** The mock layer pages at fifty at most; a headline count has to walk the pages to be counted. */
const PAGE = 50;

async function drain<T>(load: (page: number) => Promise<Page<T>>): Promise<T[]> {
  const first = await load(1);
  const items = [...first.items];
  const size = first.pageSize || PAGE;
  const lastPage = Math.max(1, Math.ceil(first.total / size));
  for (let page = 2; page <= lastPage; page += 1) {
    const next = await load(page);
    items.push(...next.items);
  }
  return items;
}

interface TallyMetric {
  key: string;
  label: string;
  icon: LucideIcon;
  /** The count the headline shows. Zero means the period holds no records for this line. */
  value: number;
  /** The change against the period before, or null when the period before held no records. */
  change: number | null;
}

interface DashboardView {
  summary: DashboardSummary;
  metrics: TallyMetric[];
  /** The scope in words: the organization's name, or the whole workspace. */
  scopeLabel: string;
}

/** The dashboard's one read: the summary for the totals and the recent list, plus the records
 *  needed to know whether the period before the selected one actually holds anything. The mock
 *  summary exposes the events count for the period (so the period before is its difference from
 *  the delta); for people, sessions and organizations the strip reads the records themselves. */
async function loadDashboard(orgId: string | null): Promise<DashboardView> {
  const summary = await dashboardSummary(orgId ?? undefined);

  const from = new Date(summary.period.from).getTime();
  const to = new Date(summary.period.to).getTime();
  const span = Math.max(1, to - from);
  const before = from - span;

  const [users, sessions, organizations] = await Promise.all([
    drain((page) => listUsers({ page, pageSize: PAGE, orgId: orgId ?? undefined })),
    drain((page) => listSessions({ page, pageSize: PAGE, orgId: orgId ?? undefined })),
    drain((page) => listOrganizations({ page, pageSize: PAGE })),
  ]);

  const inWindow = (iso: string, start: number, end: number) => {
    const at = new Date(iso).getTime();
    return at >= start && at < end;
  };

  const prior = {
    users: users.some((user) => inWindow(user.createdAt, before, from)),
    sessions: sessions.some((session) => inWindow(session.startedAt, before, from)),
    organizations: organizations.some((org) => inWindow(org.createdAt, before, from)),
    events: summary.events.total - summary.events.delta > 0,
  };

  const metrics: TallyMetric[] = [
    {
      key: "users",
      label: "Users",
      icon: Users,
      value: summary.users.total,
      change: prior.users ? summary.users.delta : null,
    },
    {
      key: "sessions",
      label: "Active sessions",
      icon: MonitorSmartphone,
      value: summary.sessions.total,
      change: prior.sessions ? summary.sessions.delta : null,
    },
    {
      key: "organizations",
      label: "Organizations",
      icon: Building,
      value: summary.organizations.total,
      change: prior.organizations ? summary.organizations.delta : null,
    },
    {
      key: "events",
      label: "Events in the period",
      icon: ScrollText,
      value: summary.events.total,
      change: prior.events ? summary.events.delta : null,
    },
  ];

  const organization = orgId ? organizations.find((org) => org.id === orgId) : undefined;

  return { summary, metrics, scopeLabel: organization ? organization.name : "the whole workspace" };
}

export function DashboardScreen() {
  return (
    <RequirePermission permission="dashboard.read" what="The overview" title="Overview">
      <DashboardBoard />
    </RequirePermission>
  );
}

function DashboardBoard() {
  const { orgId, sweepKey } = useScope();
  const dashboard = useQuery({
    queryKey: ["dashboard", orgId ?? "all"],
    queryFn: () => loadDashboard(orgId),
  });

  const summary = dashboard.data?.summary;
  const metrics = dashboard.data?.metrics ?? [];
  const scopeLabel = dashboard.data?.scopeLabel ?? (orgId ? "this organization" : "the whole workspace");
  const period = summary
    ? `${dateTime(summary.period.from)} – ${dateTime(summary.period.to)}`
    : "the last 30 days";

  return (
    <>
      <PageHeader
        title="Overview"
        count={summary ? plural(summary.events.total, "event", "events") : undefined}
        description={`The state of ${scopeLabel} for the last 30 days. Each headline count carries its change against the 30 days before it.`}
        crumbs={[{ label: "Board", to: "/" }, { label: "Overview" }]}
      />

      <div className="flex min-w-0 flex-col gap-3 px-3 py-3 sm:px-5">
        {dashboard.isLoading ? (
          <Panel>
            <LoadingRows rows={4} />
          </Panel>
        ) : dashboard.isError ? (
          <Panel>
            <ErrorState
              title="The overview did not load"
              error={dashboard.error}
              onRetry={() => void dashboard.refetch()}
            />
          </Panel>
        ) : (
          <>
            <TallyStrip metrics={metrics} period={period} scopeLabel={scopeLabel} />

            <div className="grid min-w-0 gap-3 lg:grid-cols-[minmax(0,1fr)_20rem]">
              <RecentActivity events={summary?.recent ?? []} sweepKey={sweepKey} />
              <QuickActions />
            </div>

            <div className="pt-1">
              <SeedNotice />
            </div>
          </>
        )}
      </div>
    </>
  );
}

/** One wide panel divided by rules into the counts — deliberately not four identical cards.
 *  Each column keeps its own comparison beneath it, so the pairing survives the narrow viewport. */
function TallyStrip({ metrics, period, scopeLabel }: { metrics: TallyMetric[]; period: string; scopeLabel: string }) {
  return (
    <Panel>
      <PanelHead
        title="Headline counts"
        description={`Recorded on ${scopeLabel}. The change beneath each count compares the last 30 days with the 30 days before (${period}).`}
      />
      <div className="grid grid-cols-2 lg:grid-cols-4">
        {metrics.map((metric, index) => (
          <div
            key={metric.key}
            className={cn(
              "flex min-w-0 flex-col gap-1.5 px-3 py-3",
              index % 2 === 1 && "border-l border-rule",
              index >= 2 && "border-t border-rule lg:border-t-0",
              index === 2 && "lg:border-l lg:border-rule",
            )}
          >
            <span className="flex items-center gap-1.5">
              <metric.icon size={12} aria-hidden="true" className="shrink-0 text-ink-muted" />
              <span className="label text-ink-muted">{metric.label}</span>
            </span>

            {metric.value > 0 ? (
              <span className="num text-title font-semibold leading-none text-ink">{number(metric.value)}</span>
            ) : (
              <span className="text-body font-semibold leading-tight text-ink-muted">No data for this period</span>
            )}

            <span className="flex min-h-5 items-center">
              {metric.value === 0 ? null : metric.change === null ? (
                <span className="text-[0.6875rem] text-ink-muted">No prior period to compare</span>
              ) : (
                <Delta value={metric.change} label="vs previous 30 days" />
              )}
            </span>
          </div>
        ))}
      </div>
    </Panel>
  );
}

function RecentActivity({ events, sweepKey }: { events: AuditEvent[]; sweepKey: number }) {
  return (
    <Panel className="min-w-0">
      <PanelHead
        title="Recent activity"
        count={plural(events.length, "entry", "entries")}
        description="The newest audit events on this scope, newest first. Open an entry for its field-level detail."
        actions={
          <Gate permission="audit.read">
            <Link
              to="/audit"
              className="text-[0.6875rem] font-semibold text-ink-muted underline decoration-rule-strong hover:text-ink hover:decoration-ink"
            >
              All events
            </Link>
          </Gate>
        }
      />

      {events.length === 0 ? (
        <EmptyState
          title="No activity on this board yet"
          body="The audit record is append-only and starts empty on a fresh board. The first state-changing action appears here, with a link to its detail."
        />
      ) : (
        <ul className="divide-y divide-rule">
          {events.map((event, index) => (
            <li key={`${sweepKey}-${event.id}`}>
              <Link
                to="/audit/$eventId"
                params={{ eventId: event.id }}
                className="sweep flex flex-wrap items-baseline gap-x-2 gap-y-0.5 px-3 py-2 hover:bg-hover sm:flex-nowrap"
                style={{ ["--sweep-index" as string]: String(Math.min(index, 12)) }}
              >
                <span
                  className="min-w-0 truncate text-body font-semibold text-ink sm:w-[9.5rem] sm:shrink-0"
                  title={event.actorName}
                >
                  {event.actorName}
                </span>
                <span className="font-mono text-[0.6875rem] text-ink-muted sm:w-[10.5rem] sm:shrink-0 sm:truncate" title={event.action}>
                  {event.action}
                </span>
                <span className="min-w-0 flex-1 truncate text-body text-ink-muted" title={event.targetLabel}>
                  {event.targetLabel}
                </span>
                <time
                  dateTime={event.at}
                  className="num ml-auto shrink-0 text-[0.6875rem] text-ink-muted sm:w-[5rem] sm:text-right"
                  title={dateTime(event.at)}
                >
                  {relativeTime(event.at)}
                </time>
                <ArrowUpRight size={12} aria-hidden="true" className="shrink-0 text-ink-muted" />
              </Link>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}

interface QuickAction {
  key: string;
  label: string;
  hint: string;
  to: "/users" | "/roles" | "/sessions" | "/audit";
  permission: string;
  icon: LucideIcon;
}

const QUICK_ACTIONS: QuickAction[] = [
  { key: "invite", label: "Invite a user", hint: "Add an account to the directory.", to: "/users", permission: "users.invite", icon: UserPlus },
  { key: "sessions", label: "Review active sessions", hint: "See where the board is signed in.", to: "/sessions", permission: "sessions.read", icon: MonitorSmartphone },
  { key: "roles", label: "Review roles", hint: "See what each role may do.", to: "/roles", permission: "roles.read", icon: ShieldCheck },
  { key: "audit", label: "Read the audit record", hint: "Follow what changed, and who changed it.", to: "/audit", permission: "audit.read", icon: ScrollText },
];

/** Quick actions are offered, not hidden behind a dead control: an action the identity may not
 *  perform stays in place, disabled, and says which permission is missing — to sight and to a
 *  screen reader alike. */
function QuickActions() {
  const { can } = useAccess();
  return (
    <Panel className="min-w-0">
      <PanelHead
        title="Quick actions"
        description="Start a common task. An action your roles do not grant states what it needs instead of failing when pressed."
      />
      <ul className="divide-y divide-rule">
        {QUICK_ACTIONS.map((action) => {
          const permitted = can(action.permission);
          const reasonId = `quick-${action.key}-reason`;
          const body = (
            <>
              <action.icon size={14} aria-hidden="true" className="mt-0.5 shrink-0 text-ink-muted" />
              <span className="min-w-0">
                <span className="block text-body font-semibold text-ink">{action.label}</span>
                <span className="block text-[0.6875rem] text-ink-muted">{action.hint}</span>
              </span>
              {permitted ? <ArrowRight size={13} aria-hidden="true" className="ml-auto shrink-0 text-ink-muted" /> : null}
            </>
          );
          return (
            <li key={action.key} className="flex flex-col">
              {permitted ? (
                <Link to={action.to} className="flex items-start gap-2 px-3 py-2 hover:bg-hover">
                  {body}
                </Link>
              ) : (
                <button
                  type="button"
                  disabled
                  aria-describedby={reasonId}
                  className="flex w-full cursor-not-allowed items-start gap-2 px-3 py-2 text-left opacity-70"
                >
                  {body}
                </button>
              )}
              {permitted ? null : (
                <p id={reasonId} className="px-3 pb-2 text-[0.6875rem] text-ink-muted">
                  Unavailable: needs <span className="font-mono text-ink">{action.permission}</span>, which your roles do
                  not grant. Ask an owner if you need it.
                </p>
              )}
            </li>
          );
        })}
      </ul>
    </Panel>
  );
}
