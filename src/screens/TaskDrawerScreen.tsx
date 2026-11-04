/** The task drawer: everything behind one task's merge, on one page. */

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { getItem, postVerdict, shortTime } from "../lib/fleet";
import {
  Badge,
  Button,
  EmptyState,
  ErrorState,
  LoadingRows,
  Panel,
  PanelHead,
  StatusMagnet,
  Textarea,
  useToast,
} from "../components/ui";
import { PageHeader } from "../shell/PageHeader";

function CheckList({ checks }: { checks: { name: string; conclusion: string }[] }) {
  if (checks.length === 0) return <p className="text-[0.6875rem] text-ink-muted">No checks recorded.</p>;
  return (
    <ul className="flex flex-col gap-1">
      {checks.map((check) => (
        <li key={check.name} className="flex items-center gap-2 text-[0.6875rem]">
          <Badge tone={check.conclusion === "success" ? "quiet" : "ink"}>{check.conclusion}</Badge>
          <span className="font-mono text-ink">{check.name}</span>
        </li>
      ))}
    </ul>
  );
}

/** The operator's own approve/reject. It writes the note a reviewer seat would write,
 *  so a human can overrule a stalled review instead of waiting for one. */
function VerdictActions({ number, change }: { number: number; change: { number: number } }) {
  const [reason, setReason] = useState("");
  const queryClient = useQueryClient();
  const toast = useToast();
  const submit = useMutation({
    mutationFn: (verdict: "accept" | "reject") => postVerdict(number, verdict, reason.trim()),
    onSuccess: (result) => {
      toast.done(
        result.verdict === "accept" ? `Approved ${result.sha}` : `Rejected ${result.sha}`,
        result.merge === "act-tick"
          ? `Noted on PR #${result.change_request}; the act tick merges on its next pass.`
          : `Noted on PR #${result.change_request}, but this item has no dispatch row: merge it by hand.`,
      );
      setReason("");
      void queryClient.invalidateQueries({ queryKey: ["item", number] });
      void queryClient.invalidateQueries({ queryKey: ["board"] });
    },
    onError: (error) => {
      toast.problem("The verdict did not stick", error instanceof Error ? error.message : String(error));
    },
  });
  const busy = submit.isPending;
  return (
    <div className="flex flex-col gap-2 border-t border-rule px-3 py-2">
      <label className="label text-ink-muted" htmlFor="verdict-reason">
        Reason — required to reject, it is the builder&apos;s work list
      </label>
      <Textarea
        id="verdict-reason"
        rows={2}
        value={reason}
        placeholder="What did you check, or what must change?"
        onChange={(event) => setReason(event.target.value)}
      />
      {submit.error ? (
        <p className="text-[0.6875rem] text-destructive">{submit.error.message}</p>
      ) : null}
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="primary" busy={busy} onClick={() => submit.mutate("accept")}>
          Approve PR #{change.number}
        </Button>
        <Button
          variant="attention"
          busy={busy}
          disabled={reason.trim() === ""}
          title={reason.trim() === "" ? "A reject needs a reason" : undefined}
          onClick={() => submit.mutate("reject")}
        >
          Reject
        </Button>
      </div>
    </div>
  );
}

export function TaskDrawerScreen() {
  const { number } = useParams({ from: "/items/$number" });
  const itemNumber = Number(number);
  const { data, error, isLoading, refetch } = useQuery({
    queryKey: ["item", itemNumber],
    queryFn: () => getItem(itemNumber),
    enabled: Number.isFinite(itemNumber),
  });

  if (isLoading) {
    return (
      <div className="px-3 py-3 sm:px-5">
        <Panel>
          <LoadingRows rows={8} />
        </Panel>
      </div>
    );
  }
  if (error || !data) {
    return (
      <div className="px-3 py-3 sm:px-5">
        <Panel>
          <ErrorState title="The task did not load" error={error} onRetry={() => void refetch()} />
        </Panel>
      </div>
    );
  }

  const { item, project, dispatch, brief, change_request: change, verdicts, events } = data;
  const latest = verdicts && verdicts.length > 0 ? verdicts[0] : null;

  return (
    <div className="flex flex-col">
      <PageHeader
        title={`#${item.number} ${item.title}`}
        count={item.state}
        crumbs={[{ label: "Board", to: "/" }, { label: `#${item.number}` }]}
        description={`${project.name} · opened ${item.age} ago`}
        actions={<StatusMagnet status={item.state} />}
      />
      <div className="grid gap-3 px-3 py-3 sm:px-5 lg:grid-cols-2">
        <Panel className="lg:col-span-1">
          <PanelHead title="Dispatch" />
          {dispatch ? (
            <div className="grid grid-cols-[8rem_1fr] gap-y-1 px-3 py-2 text-[0.6875rem]">
              <span className="label text-ink-muted">Seat</span>
              <span className="font-mono text-ink">{dispatch.seat || "—"}</span>
              <span className="label text-ink-muted">Branch</span>
              <span className="font-mono text-ink">{dispatch.branch || "—"}</span>
              <span className="label text-ink-muted">Worktree</span>
              <span className="break-all font-mono text-ink">{dispatch.worktree || "—"}</span>
              <span className="label text-ink-muted">Base sha</span>
              <span className="font-mono text-ink">{dispatch.base_sha || "—"}</span>
              <span className="label text-ink-muted">Ended</span>
              <span className="text-ink">{dispatch.ended_at ? shortTime(dispatch.ended_at) : "in flight"}</span>
            </div>
          ) : (
            <EmptyState title="No dispatch" body="This task has never been handed to a seat." />
          )}
        </Panel>

        <Panel>
          <PanelHead title="Change request" />
          <div className="flex flex-col gap-2 px-3 py-2">
            {change ? (
              <>
                <p className="text-body text-ink">
                  PR #{change.number}
                  {item.url ? (
                    <a className="ml-2 text-[0.6875rem] underline decoration-rule-strong" href={item.url}>
                      item
                    </a>
                  ) : null}
                </p>
                <p className="label text-ink-muted">Checks at the judged head</p>
                <CheckList checks={change.checks} />
              </>
            ) : (
              <p className="text-[0.6875rem] text-ink-muted">No change request recorded on this item.</p>
            )}
          </div>
        </Panel>

        <Panel className="lg:col-span-2">
          <PanelHead title="Verdict" description="The newest verdict is the one that gates a merge." />
          <div className="px-3 py-2">
            {latest ? (
              <div className="grid grid-cols-[8rem_1fr] gap-y-1 text-[0.6875rem]">
                <span className="label text-ink-muted">Decision</span>
                <span className="font-semibold text-ink">{latest.decision}</span>
                <span className="label text-ink-muted">Sha</span>
                <span className="font-mono text-ink">{latest.sha}</span>
                <span className="label text-ink-muted">Source</span>
                <span className="text-ink">{latest.source}</span>
                <span className="label text-ink-muted">At</span>
                <span className="text-ink">{shortTime(latest.at)}</span>
              </div>
            ) : (
              <p className="text-[0.6875rem] text-ink-muted">No verdict yet.</p>
            )}
          </div>
          {change ? <VerdictActions number={item.number} change={change} /> : null}
        </Panel>

        <Panel className="lg:col-span-2">
          <PanelHead title="Brief" description="The handoff the seat started from." />
          {brief ? (
            <pre className="max-h-72 overflow-auto px-3 py-2 font-mono text-[0.6875rem] leading-relaxed whitespace-pre-wrap text-ink">
              {brief}
            </pre>
          ) : (
            <p className="px-3 py-2 text-[0.6875rem] text-ink-muted">No brief file recorded.</p>
          )}
        </Panel>

        <Panel className="lg:col-span-2">
          <PanelHead title="Events" count={events?.length ?? 0} />
          {events && events.length > 0 ? (
            <ul className="divide-y divide-[var(--rule)]">
              {events.map((event) => (
                <li key={event.id} className="grid grid-cols-[7rem_9rem_1fr] gap-2 px-3 py-[var(--density-row-pad-y)] text-[0.6875rem]">
                  <span className="num text-ink-muted">{shortTime(event.at)}</span>
                  <span className="font-mono text-ink">{event.kind}</span>
                  <span className="text-ink-muted">{event.detail || event.subject}</span>
                </li>
              ))}
            </ul>
          ) : (
            <p className="px-3 py-2 text-[0.6875rem] text-ink-muted">No events name this item.</p>
          )}
        </Panel>
      </div>
    </div>
  );
}
