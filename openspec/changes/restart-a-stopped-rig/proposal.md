# Proposal — a stopped rig is not a quiet rig

## Why

On 2026-11-13 the fleet sat idle for 90 minutes with three ready issues. The rig was stopped, the daemon was healthy, and every tick printed `dispatches=0 skipped=0` — no intent, no refusal, no lifecycle line.

The lifecycle rule already knows how to start a stopped rig with work: `startReason` (`internal/act/lifecycle.go:335-346`) returns "N ready item(s) and no running seat" whenever a fresh managed project has ready items and `Running == 0`. It did not fire, and the two ways it can fail both leave nothing in the journal:

1. **The roster was not known.** `DecideLifecycle` (`lifecycle.go:250-253`) returns early for every rig when `!rigEntry.SeatsKnown`, and the tick passes `!read.RigStale` (`cmd/fleet-console/main.go:852`). So one failed rig read disables the start half — the only half that could repair the situation — and the stop half too, silently. A stopped rig cannot make its own read succeed.
2. **Demand was there but nothing said so.** The dispatch half then produced `ReasonNoFreeBuilder` for each ready item (`decide.go:380-390`), which is a `Skip` that act never prints. `skipped=0` in the log is about merge refusals, not plan refusals.

Either way the operator's only signal is silence, and the only repair is a human running `rig start`.

## What this changes

- **The start half stops depending on a fresh roster.** When the read is stale but the last known state had no running seat and a fresh managed project has ready items, the rig is started once, with its own backoff and its own reason, rather than decided as "unknown".
- **Every rig decision is one log line**, including "none", with the numbers that produced it (`running`, `ready`, `seats_known`, `reason`). Silence stops being a possible outcome of the lifecycle rule.
- **Plan skips reach the journal** once per item and reason — a ready item that is not dispatched says why.

## Scope

`internal/act/lifecycle.go` (the rule and the executor), `cmd/fleet-console/main.go` (the facts passed in and the log line), `internal/act/tick.go` (printing plan skips), tests. No change to the quiet-stop thresholds, no new config key, no schema change.

## Non-goals

- Not removing the quiet stop: a rig left up overnight burns tokens.
- Not restarting a rig the operator stopped on purpose (`rig down`, `rig seat stop`): that decision belongs to the human, and the current rule's escape hatch stays.
- Not fixing the stale read itself (epic #320 covers daemon-down); this change makes the loop survive one.
