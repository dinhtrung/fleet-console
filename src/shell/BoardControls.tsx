/** The seam controls: the failure switch, the latency switch and the reset.
 *
 *  The mock layer can fail on purpose, but never at random — an empty list and a broken request must
 *  look different. These controls are the only way to ask for a failure, and they say what they did. */

import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Database, TriangleAlert } from "lucide-react";
import { configure, currentRuntime, databaseInfo, resetDatabase, type FailureMode } from "../mock/api";
import {
  ConfirmDialog,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
  IconButton,
  useToast,
} from "../components/ui";
import { dateTime } from "../lib/format";

export function BoardControls() {
  const [failure, setFailureState] = useState<FailureMode>("off");
  const [latency, setLatencyState] = useState(() => currentRuntime().latencyMs);
  const [resetOpen, setResetOpen] = useState(false);
  const queryClient = useQueryClient();
  const toast = useToast();
  const info = databaseInfo();

  const setFailure = (mode: FailureMode) => {
    configure({ failure: mode });
    setFailureState(mode);
    // Force the armed failure to show immediately: with cached queries the board would serve its data
    // and the switch would look like it did nothing.
    void queryClient.invalidateQueries();
    toast.problem(
      mode === "off"
        ? "Failure switch off"
        : mode === "next"
          ? "The next request will fail"
          : "Every request will fail",
      mode === "off"
        ? undefined
        : "The board is refetching now. Turn the switch off, or use the retry path on the failed view.",
    );
  };

  const toggleLatency = () => {
    const next = latency > 0 ? 0 : 160;
    configure({ latencyMs: next });
    setLatencyState(next);
    void queryClient.invalidateQueries();
    toast.done(next === 0 ? "Latency removed for this session" : "Latency restored (160 ms)");
  };

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <IconButton
              label={failure === "off" ? "Board controls" : "Board controls — requests are set to fail"}
              className="text-rail-ink hover:bg-rail-ink/15"
            >
              <span className="relative inline-flex">
                <Database size={14} />
                {failure !== "off" ? (
                  <span aria-hidden="true" className="absolute -top-1 -right-1 block size-1.5 bg-attention" />
                ) : null}
              </span>
            </IconButton>
          }
        />
        <DropdownMenuContent align="end">
          <DropdownMenuItem onClick={() => setFailure("next")}>Fail the next request</DropdownMenuItem>
          <DropdownMenuItem variant="destructive" onClick={() => setFailure("always")}>
            Fail every request
          </DropdownMenuItem>
          <DropdownMenuItem disabled={failure === "off"} onClick={() => setFailure("off")}>
            Stop failing
          </DropdownMenuItem>
          <DropdownMenuItem onClick={toggleLatency}>
            {latency > 0 ? "Remove simulated latency" : "Restore simulated latency"}
          </DropdownMenuItem>
          <DropdownMenuItem variant="destructive" onClick={() => setResetOpen(true)}>
            Reset the board…
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <ConfirmDialog
        open={resetOpen}
        onClose={() => setResetOpen(false)}
        onConfirm={() => {
          resetDatabase();
          void queryClient.invalidateQueries();
          setResetOpen(false);
          toast.done("Board reset to its seed", `Seeded ${dateTime(info.seededAt)}`);
        }}
        title="Reset the board?"
        description="Every change you made in this browser is discarded and the deterministic seed is written back. The audit record is reset with it."
        confirmLabel="Reset the board"
        requireTyped="reset"
        typedLabel="Type reset to confirm"
      />
    </>
  );
}

/** A one-line statement of what is on the board, so the demo is never mistaken for real data. */
export function SeedNotice() {
  const info = databaseInfo();
  return (
    <p className="flex items-start gap-1.5 text-[0.6875rem] text-ink-muted">
      <TriangleAlert size={12} aria-hidden="true" className="mt-0.5 shrink-0" />
      <span>
        Synthetic data — {info.records.toLocaleString("en-GB")} records seeded {dateTime(info.seededAt)}. No real
        person or organization appears here.
      </span>
    </p>
  );
}
