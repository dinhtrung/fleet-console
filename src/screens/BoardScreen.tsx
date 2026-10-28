/** The board: every open task, its state, and the reason it is not moving. */

import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { getBoard } from "../lib/fleet";
import { shortTime } from "../lib/fleet";
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

function countLine(project: { counts: { building: number; review: number; waiting: number; parked: number; blocked: number } }): string {
  const parts = [
    project.counts.building ? `${project.counts.building} building` : "",
    project.counts.review ? `${project.counts.review} review` : "",
    project.counts.parked ? `${project.counts.parked} parked` : "",
    project.counts.waiting ? `${project.counts.waiting} waiting` : "",
    project.counts.blocked ? `${project.counts.blocked} blocked` : "",
  ].filter(Boolean);
  return parts.length > 0 ? parts.join(" · ") : "nothing open";
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
                  project.paused ? (
                    <Badge tone="tint" title="Dispatch is paused for this project">
                      paused
                    </Badge>
                  ) : (
                    <Badge tone="quiet">active</Badge>
                  )
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
                          </td>
                          <td className="px-3 py-[var(--density-row-pad-y)] text-ink-muted">{item.seat ?? "—"}</td>
                          <td className="num px-3 py-[var(--density-row-pad-y)] text-ink-muted">{item.age}</td>
                          <td
                            className={
                              item.state === "parked" || item.state === "paused"
                                ? "px-3 py-[var(--density-row-pad-y)] text-ink"
                                : "px-3 py-[var(--density-row-pad-y)] text-ink-muted"
                            }
                          >
                            {item.reason || "moving"}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </Panel>
          ))
        )}
      </div>
    </div>
  );
}
