/** The board: every open task, its state, and the reason it is not moving. */

import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { getBoard } from "../lib/fleet";
import type { BoardItem, BoardQuestion, MirrorRead } from "../lib/fleet";
import { shortTime } from "../lib/fleet";
import { dateTime } from "../lib/format";
import {
  Badge,
  EmptyState,
  ErrorState,
  LoadingRows,
  Panel,
  PanelHead,
  StatusMagnet,
} from "../components/ui";
import { PageHeader } from "../shell/PageHeader";

function countLine(project: { counts: { building: number; review: number; waiting: number; parked: number; blocked: number; stale: number } }): string {
  const parts = [
    project.counts.building ? `${project.counts.building} building` : "",
    project.counts.review ? `${project.counts.review} review` : "",
    project.counts.stale ? `${project.counts.stale} stale` : "",
    project.counts.parked ? `${project.counts.parked} parked` : "",
    project.counts.waiting ? `${project.counts.waiting} waiting` : "",
    project.counts.blocked ? `${project.counts.blocked} blocked` : "",
  ].filter(Boolean);
  return parts.length > 0 ? parts.join(" · ") : "nothing open";
}

/** One project header read line (#394). The phrase is the server's, so the age and the
 *  quiet/stalled threshold are not re-derived here; the title carries the exact stamp the age
 *  was measured from. Staleness uses the one committed attention hue, since a stalled mirror is
 *  the loudest fact in this header; a quiet read is ink, a fresh one is muted. */
function MirrorReadLine({ read }: { read: MirrorRead }) {
  const tone =
    read.state === "stalled" ? "text-attention" : read.state === "quiet" ? "text-ink" : "text-ink-muted";
  return (
    <span className={`text-[0.6875rem] ${tone}`} title={read.at ? dateTime(read.at) : "The mirror has not completed this read yet."}>
      {read.label}
    </span>
  );
}

/** The project's unanswered operator questions (#377): a note the merge gate could not read as a
 *  verdict, waiting on a person. The ledger is the source, so the list survives a restart; the
 *  queue row decides whether the question is still live, and a finished row never reaches here.
 *  With none waiting the section is one line, not an empty table — the point is that "nothing is
 *  waiting" is stated, so a stuck project cannot pass for an idle one. */
function QuestionsSection({ questions }: { questions: BoardQuestion[] }) {
  if (questions.length === 0) {
    return <p className="border-t border-rule px-3 py-2 text-[0.6875rem] text-ink-muted">No question is waiting on a person.</p>;
  }
  return (
    <div className="border-t border-rule">
      <div className="flex items-baseline gap-2 px-3 pt-2">
        <h3 className="label font-medium text-ink-muted">Waiting on a person</h3>
        <span className="num text-[0.6875rem] text-ink-muted">{questions.length}</span>
      </div>
      <div className="overflow-x-auto">
        <table className="w-full border-collapse text-body">
          <thead>
            <tr className="border-b border-rule text-left">
              <th className="label px-3 py-1.5 font-medium text-ink-muted">Item</th>
              <th className="label px-3 py-1.5 font-medium text-ink-muted">Change request</th>
              <th className="label px-3 py-1.5 font-medium text-ink-muted">Note</th>
              <th className="label px-3 py-1.5 font-medium text-ink-muted">Head</th>
              <th className="label px-3 py-1.5 font-medium text-ink-muted">Waiting</th>
              <th className="label px-3 py-1.5 font-medium text-ink-muted">Asked</th>
            </tr>
          </thead>
          <tbody>
            {questions.map((question) => (
              <tr key={question.qitem ?? `${question.item}-${question.note_id}`} className="border-b border-rule last:border-0">
                <td className="px-3 py-[var(--density-row-pad-y)]">
                  <Link
                    to="/items/$number"
                    params={{ number: String(question.item) }}
                    className="num text-ink hover:underline"
                  >
                    #{question.item}
                  </Link>
                </td>
                <td className="num px-3 py-[var(--density-row-pad-y)] text-ink-muted">#{question.change_request}</td>
                <td className="num px-3 py-[var(--density-row-pad-y)] text-ink-muted">{question.note_id}</td>
                <td className="num px-3 py-[var(--density-row-pad-y)] text-ink-muted">{question.head}</td>
                <td className="num px-3 py-[var(--density-row-pad-y)] text-ink">{question.waiting}</td>
                <td className="px-3 py-[var(--density-row-pad-y)] text-ink-muted">{question.destination}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}


/** The why-column (#429). Three facts, in this order, and never a blank cell:
 *
 *  1. the last plan's recorded reason and detail (`plan_reason`, #428) — the durable answer, which
 *     survives the restart that erases a tick's log;
 *  2. the state-derived reason the row already carries (a stale verdict, a refused merge);
 *  3. "no plan has run since start" when the project has never been planned — the empty reason
 *     table then says nothing at all, and a blank cell would read as "nothing wrong".
 *
 *  A project that has been planned but carries no reason for this item says "moving": the last
 *  plan had nothing to say about it. */
function WhyCell({ item, planned }: { item: BoardItem; planned: boolean }) {
  if (item.plan_reason) {
    return (
      <>
        <span className="text-ink">{item.plan_reason}</span>
        {item.plan_age ? (
          <span className="num ml-2 text-[0.625rem] text-ink-muted">recorded {item.plan_age} ago</span>
        ) : null}
      </>
    );
  }
  if (item.reason) return <>{item.reason}</>;
  if (!planned) {
    return <span className="text-ink-muted">no plan has run since start</span>;
  }
  return <>moving</>;
}

export function BoardScreen() {
  const { data, error, isLoading, refetch } = useQuery({
    queryKey: ["board"],
    queryFn: getBoard,
    refetchInterval: 15000,
  });

  return (
    <div className="flex flex-col">
      <PageHeader
        title="Board"
        count={data ? `${data.projects.reduce((sum, p) => sum + p.items.length, 0)} open` : undefined}
        description="Projects, their open tasks, and the fact that is stopping each one."
        actions={
          data ? <span className="text-[0.6875rem] text-ink-muted">read {shortTime(data.synced_at)}</span> : null
        }
      />
      <div className="flex flex-col gap-3 px-3 py-3 sm:px-5">
        {isLoading ? (
          <Panel>
            <LoadingRows rows={6} />
          </Panel>
        ) : error ? (
          <Panel>
            <ErrorState title="The board did not load" error={error} onRetry={() => void refetch()} />
          </Panel>
        ) : !data || data.projects.length === 0 ? (
          <Panel>
            <EmptyState title="No projects" body="Add a project row to render its rig and show its board." />
          </Panel>
        ) : (
          data.projects.map((project) => (
            <Panel key={project.key}>
              <PanelHead
                title={project.name}
                count={project.items.length}
                description={countLine(project)}
                actions={
                  <div className="flex flex-wrap items-center gap-2">
                    <MirrorReadLine read={project.forge_read} />
                    <MirrorReadLine read={project.rig_read} />
                    {project.paused ? (
                      <Badge tone="tint" title="Dispatch is paused for this project">
                        paused
                      </Badge>
                    ) : (
                      <Badge tone="quiet">active</Badge>
                    )}
                  </div>
                }
              />
              {project.items.length === 0 ? (
                <EmptyState title="Nothing open" body="Every task in this project is closed." />
              ) : (
                <div className="overflow-x-auto">
                  <table className="w-full border-collapse text-body">
                    <thead>
                      <tr className="border-b border-rule text-left">
                        <th className="label px-3 py-1.5 font-medium text-ink-muted">#</th>
                        <th className="label px-3 py-1.5 font-medium text-ink-muted">Task</th>
                        <th className="label px-3 py-1.5 font-medium text-ink-muted">State</th>
                        <th className="label px-3 py-1.5 font-medium text-ink-muted">Seat</th>
                        <th className="label px-3 py-1.5 font-medium text-ink-muted">Age</th>
                        <th className="label px-3 py-1.5 font-medium text-ink-muted">Why it is not moving</th>
                      </tr>
                    </thead>
                    <tbody>
                      {project.items.map((item) => (
                        <tr key={item.number} className="border-b border-rule last:border-0">
                          <td className="num px-3 py-[var(--density-row-pad-y)] text-ink-muted">{item.number}</td>
                          <td className="px-3 py-[var(--density-row-pad-y)]">
                            <Link
                              to="/items/$number"
                              params={{ number: String(item.number) }}
                              className="text-ink hover:underline"
                            >
                              {item.title}
                            </Link>
                          </td>
                          <td className="px-3 py-[var(--density-row-pad-y)]">
                            <StatusMagnet status={item.state} />
                            {item.verdict_sha && item.head_sha ? (
                              // console-ui: both commits on the row, not buried in a sentence.
                              <span className="mt-0.5 block font-mono text-[0.625rem] leading-tight text-ink-muted">
                                {item.verdict_sha} → {item.head_sha}
                              </span>
                            ) : null}
                          </td>
                          <td className="px-3 py-[var(--density-row-pad-y)] text-ink-muted">{item.seat ?? "—"}</td>
                          <td className="num px-3 py-[var(--density-row-pad-y)] text-ink-muted">{item.age}</td>
                          <td
                            className={
                              item.state === "parked" || item.state === "paused" || item.state === "stale"
                                ? "px-3 py-[var(--density-row-pad-y)] text-ink"
                                : "px-3 py-[var(--density-row-pad-y)] text-ink-muted"
                            }
                          >
                            <WhyCell item={item} planned={project.planned} />
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
              <QuestionsSection questions={project.questions} />
            </Panel>
          ))
        )}
      </div>
    </div>
  );
}
