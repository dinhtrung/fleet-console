/** Seats: who is running, on what model, and what their pane last said. */

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { getSeats, seatTail, shortTime, type Seat } from "../lib/fleet";
import {
  Badge,
  EmptyState,
  ErrorState,
  LoadingRows,
  Panel,
  SidePanel,
  StatusMagnet,
} from "../components/ui";
import { PageHeader } from "../shell/PageHeader";

export function SeatsScreen() {
  const [selected, setSelected] = useState<Seat | null>(null);
  const seats = useQuery({ queryKey: ["seats"], queryFn: getSeats, refetchInterval: 10000 });
  const tail = useQuery({
    queryKey: ["seat-tail", selected?.session],
    queryFn: () => seatTail(selected!.session, 24),
    enabled: selected !== null,
  });

  return (
    <div className="flex flex-col">
      <PageHeader
        title="Seats"
        count={seats.data?.seats.length}
        description="Live topology from rig ps: state, observed model, permission profile and last activity."
      />
      <div className="px-3 py-3 sm:px-5">
        <Panel>
          {seats.isLoading ? (
            <LoadingRows rows={5} />
          ) : seats.error ? (
            <ErrorState title="Seats did not load" error={seats.error} onRetry={() => void seats.refetch()} />
          ) : !seats.data || seats.data.seats.length === 0 ? (
            <EmptyState title="No seats" body="No rig is running on this box." />
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full border-collapse text-body">
                <thead>
                  <tr className="border-b border-rule text-left">
                    <th className="label px-3 py-1.5 font-medium text-ink-muted">Session</th>
                    <th className="label px-3 py-1.5 font-medium text-ink-muted">State</th>
                    <th className="label px-3 py-1.5 font-medium text-ink-muted">Model</th>
                    <th className="label px-3 py-1.5 font-medium text-ink-muted">Permission</th>
                    <th className="label px-3 py-1.5 font-medium text-ink-muted">Last activity</th>
                  </tr>
                </thead>
                <tbody>
                  {seats.data.seats.map((seat) => (
                    <tr
                      key={seat.session}
                      className="cursor-pointer border-b border-rule last:border-0 hover:bg-hover"
                      onClick={() => setSelected(seat)}
                      tabIndex={0}
                      onKeyDown={(event) => {
                        if (event.key === "Enter" || event.key === " ") setSelected(seat);
                      }}
                    >
                      <td className="px-3 py-[var(--density-row-pad-y)]">
                        <span className="font-mono text-[0.6875rem] text-ink">{seat.session}</span>
                        <span className="ml-2 text-[0.6875rem] text-ink-muted">{seat.rig}</span>
                      </td>
                      <td className="px-3 py-[var(--density-row-pad-y)]">
                        <StatusMagnet status={seat.state} />
                      </td>
                      <td className="px-3 py-[var(--density-row-pad-y)] font-mono text-[0.6875rem] text-ink">
                        {seat.model || "—"}
                      </td>
                      <td className="px-3 py-[var(--density-row-pad-y)]">
                        <Badge tone="quiet">{seat.permission || "—"}</Badge>
                      </td>
                      <td className="num px-3 py-[var(--density-row-pad-y)] text-ink-muted">
                        {shortTime(seat.last_activity)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Panel>
      </div>

      <SidePanel
        open={selected !== null}
        onClose={() => setSelected(null)}
        title={selected?.session ?? "Seat"}
        description={selected ? `${selected.rig} · ${selected.runtime}` : undefined}
      >
        {selected ? (
          <div className="flex flex-col gap-3">
            <dl className="grid grid-cols-[7rem_1fr] gap-y-1 text-[0.6875rem]">
              <dt className="label text-ink-muted">State</dt>
              <dd className="text-ink">{selected.state}</dd>
              <dt className="label text-ink-muted">Model</dt>
              <dd className="font-mono text-ink">{selected.model || "—"}</dd>
              <dt className="label text-ink-muted">Permission</dt>
              <dd className="text-ink">{selected.permission || "—"}</dd>
              <dt className="label text-ink-muted">Last activity</dt>
              <dd className="text-ink">{shortTime(selected.last_activity)}</dd>
            </dl>
            <div>
              <p className="label mb-1 text-ink-muted">Pane tail (read-only)</p>
              {tail.isLoading ? (
                <LoadingRows rows={5} />
              ) : tail.error ? (
                <ErrorState title="The pane did not answer" error={tail.error} onRetry={() => void tail.refetch()} />
              ) : (
                <pre className="max-h-[24rem] overflow-auto border border-rule bg-ground/40 p-2 font-mono text-[0.6875rem] leading-relaxed whitespace-pre-wrap text-ink">
                  {tail.data?.tail || "(empty pane)"}
                </pre>
              )}
            </div>
          </div>
        ) : null}
      </SidePanel>
    </div>
  );
}
