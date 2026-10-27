/** One audit event, field by field.
 *
 *  The detail shows what the action did: the before and after of every affected field, marked as an
 *  addition, a removal or a change, alongside the actor, the IP and the exact time. It is read-only
 *  by construction — there is no control here that could edit or delete the event. */

import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { ArrowRight, Equal, Minus, Pencil, Plus } from "lucide-react";
import { Badge, ErrorState, FieldRow, LoadingRows, NotFoundState, Panel, PanelHead } from "../components/ui";
import { RequirePermission } from "../components/RequirePermission";
import { PageHeader } from "../shell/PageHeader";
import { getAuditEvent } from "../mock/api";
import { isApiError, type AuditEvent, type FieldChange } from "../mock/types";
import { cn } from "../lib/cn";
import { dateTime, orDash, plural, relativeTime } from "../lib/format";

export function AuditDetailScreen({ eventId }: { eventId: string }) {
  return (
    <RequirePermission permission="audit.read" what="This audit event" title="Audit event">
      <AuditEventBody eventId={eventId} />
    </RequirePermission>
  );
}

/** The target names a user, an organization or a role: link to it where such a surface exists. */
function TargetLink({ event }: { event: AuditEvent }) {
  const className = "text-ink underline decoration-rule-strong hover:decoration-ink";
  switch (event.targetType) {
    case "user":
      return (
        <Link to="/users/$userId" params={{ userId: event.targetId }} className={className}>
          {event.targetLabel}
        </Link>
      );
    case "organization":
      return (
        <Link to="/organizations/$orgId" params={{ orgId: event.targetId }} className={className}>
          {event.targetLabel}
        </Link>
      );
    case "role":
      return (
        <Link to="/roles/$roleId" params={{ roleId: event.targetId }} className={className}>
          {event.targetLabel}
        </Link>
      );
    case "settings":
      return (
        <Link to="/settings" className={className}>
          {event.targetLabel}
        </Link>
      );
    default:
      return <span className="text-ink">{event.targetLabel}</span>;
  }
}

function ChangeRow({ change }: { change: FieldChange }) {
  const added = change.before === null;
  const removed = change.after === null;
  const unchanged = !added && !removed && change.before === change.after;
  const kind = added ? "Added" : removed ? "Removed" : unchanged ? "Unchanged" : "Changed";
  const Icon = added ? Plus : removed ? Minus : unchanged ? Equal : Pencil;

  return (
    <li className="flex flex-col gap-1 border-b border-rule px-3 py-2 last:border-b-0">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-mono text-[0.6875rem] font-semibold text-ink-muted">{change.field}</span>
        <Badge tone="quiet">
          <Icon size={11} aria-hidden="true" /> {kind}
        </Badge>
      </div>
      <div className="flex flex-wrap items-center gap-2 font-mono text-body">
        <span className={cn("break-words text-ink-muted", removed && "line-through")}>{orDash(change.before)}</span>
        <ArrowRight size={12} aria-hidden="true" className="shrink-0 text-ink-muted" />
        <span className={cn("break-words", removed ? "text-ink-muted" : "text-ink")}>{orDash(change.after)}</span>
      </div>
    </li>
  );
}

function AuditEventBody({ eventId }: { eventId: string }) {
  const query = useQuery({ queryKey: ["audit-event", eventId], queryFn: () => getAuditEvent(eventId) });

  const header = (
    <PageHeader
      title="Audit event"
      crumbs={[{ label: "Board", to: "/" }, { label: "Audit record", to: "/audit" }, { label: eventId }]}
    />
  );

  if (query.isLoading) {
    return (
      <>
        {header}
        <div className="px-3 sm:px-5">
          <Panel>
            <LoadingRows rows={5} />
          </Panel>
        </div>
      </>
    );
  }

  if (query.error) {
    const missing = isApiError(query.error) && query.error.code === "not_found";
    return (
      <>
        {header}
        <div className="px-3 sm:px-5">
          {missing ? (
            <NotFoundState what="audit event">
              <Link to="/audit" className="text-ink underline decoration-rule-strong hover:decoration-ink">
                Back to the audit record
              </Link>
            </NotFoundState>
          ) : (
            <ErrorState error={query.error} onRetry={() => void query.refetch()} />
          )}
        </div>
      </>
    );
  }

  const event = query.data;
  if (!event) return <>{header}</>;

  return (
    <>
      {header}
      <div className="flex flex-col gap-3 px-3 py-3 sm:px-5">
        <Panel>
          <PanelHead
            title="Event"
            count={relativeTime(event.at)}
            description="Append-only. This event cannot be edited or removed."
          />
          <div className="grid gap-x-6 px-3 py-2 sm:grid-cols-2">
            <FieldRow label="Actor">
              <span className="text-ink">{event.actorName}</span>
              <span className="block font-mono text-[0.6875rem] text-ink-muted">{event.actorId}</span>
            </FieldRow>
            <FieldRow label="Action">
              <span className="font-mono">{event.action}</span>
            </FieldRow>
            <FieldRow label="Target">
              <TargetLink event={event} />
              <span className="block font-mono text-[0.6875rem] text-ink-muted">{event.targetId}</span>
            </FieldRow>
            <FieldRow label="IP address">
              <span className="font-mono">{event.ip}</span>
            </FieldRow>
            <FieldRow label="Recorded">
              <span className="text-ink">{dateTime(event.at)}</span>
              <span className="block font-mono text-[0.6875rem] text-ink-muted">{event.at}</span>
            </FieldRow>
            <FieldRow label="Event reference">
              <span className="font-mono">{event.id}</span>
            </FieldRow>
          </div>
        </Panel>

        <Panel>
          <PanelHead title="Field-level change" count={plural(event.changes.length, "field")} />
          {event.changes.length === 0 ? (
            <p className="px-3 py-3 text-body text-ink-muted">
              This event did not change a field. It is recorded so the action is not lost from the record.
            </p>
          ) : (
            <ul>
              {event.changes.map((change, index) => (
                <ChangeRow key={`${change.field}-${index}`} change={change} />
              ))}
            </ul>
          )}
        </Panel>
      </div>
    </>
  );
}
