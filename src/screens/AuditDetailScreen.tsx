/** One event, field by field, exactly as the log recorded it.
 *
 *  The row carries the actor, kind, subject, detail, timestamp and — when the event ran a command —
 *  its exit code and stdout hash. Design D3 keeps it to those fields: the hash is what the audit
 *  contract records, and pretending to re-read the command's output would be a storage promise the
 *  app does not make. Read-only by construction: nothing here can edit or delete the event. */

import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { ErrorState, FieldRow, LoadingRows, NotFoundState, Panel, PanelHead } from "../components/ui";
import { PageHeader } from "../shell/PageHeader";
import { getEvent, isApiError } from "../lib/fleet";
import { dateTime, orDash, relativeTime } from "../lib/format";

export function AuditDetailScreen({ eventId }: { eventId: string }) {
  return <AuditEventBody eventId={eventId} />;
}

function AuditEventBody({ eventId }: { eventId: string }) {
  // The route param is a string; the API's id is an integer. A param that is not one cannot name a
  // row, so it is a 404 rather than a request the server would answer with its own 404 anyway.
  const id = Number(eventId);
  const valid = Number.isInteger(id) && id > 0;

  const query = useQuery({
    queryKey: ["audit-event", id],
    queryFn: () => getEvent(id),
    enabled: valid,
  });

  const header = (
    <PageHeader
      title="Audit event"
      crumbs={[{ label: "Board", to: "/" }, { label: "Audit record", to: "/audit" }, { label: eventId }]}
    />
  );

  if (!valid) {
    return (
      <>
        {header}
        <div className="px-3 sm:px-5">
          <NotFoundState what="audit event">
            <BackLink />
          </NotFoundState>
        </div>
      </>
    );
  }

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
    // A 404 is "no such event" and nothing else; anything else is a read that failed, and it keeps
    // the retry. The two must not collapse into one state.
    const missing = isApiError(query.error) && query.error.status === 404;
    return (
      <>
        {header}
        <div className="px-3 sm:px-5">
          {missing ? (
            <NotFoundState what="audit event">
              <BackLink />
            </NotFoundState>
          ) : (
            <ErrorState
              title="The audit event could not be read"
              error={query.error}
              onRetry={() => void query.refetch()}
            />
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
              <span className="font-mono">{event.actor}</span>
            </FieldRow>
            <FieldRow label="Kind">
              <span className="font-mono">{event.kind}</span>
            </FieldRow>
            <FieldRow label="Subject">
              <span className="font-mono">{orDash(event.subject)}</span>
            </FieldRow>
            <FieldRow label="Recorded">
              <span className="text-ink">{dateTime(event.at)}</span>
              <span className="block font-mono text-[0.6875rem] text-ink-muted">{event.at}</span>
            </FieldRow>
            <FieldRow label="Event reference">
              <span className="num">{event.id}</span>
            </FieldRow>
          </div>
        </Panel>

        <Panel>
          <PanelHead title="Detail" />
          {event.detail ? (
            <pre className="overflow-x-auto px-3 py-2 font-mono text-[0.75rem] whitespace-pre-wrap text-ink">
              {event.detail}
            </pre>
          ) : (
            <p className="px-3 py-3 text-body text-ink-muted">
              This event recorded no detail. It is still the whole record of what happened.
            </p>
          )}
        </Panel>

        {/* The command's outcome only exists when the event ran one: an exit code is present or it is
            not, and a stdout hash is only meaningful next to it. */}
        {event.exit_code !== undefined || event.stdout_hash ? (
          <Panel>
            <PanelHead title="Command" description="What the recorded command returned." />
            <div className="grid gap-x-6 px-3 py-2 sm:grid-cols-2">
              <FieldRow label="Exit code">
                <span className="num">{event.exit_code === undefined ? "—" : event.exit_code}</span>
              </FieldRow>
              <FieldRow label="stdout hash">
                <span className="font-mono text-[0.6875rem] break-all">{orDash(event.stdout_hash)}</span>
              </FieldRow>
            </div>
          </Panel>
        ) : null}
      </div>
    </>
  );
}

function BackLink() {
  return (
    <Link to="/audit" className="text-ink underline decoration-rule-strong hover:decoration-ink">
      Back to the audit record
    </Link>
  );
}
