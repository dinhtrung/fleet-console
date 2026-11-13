# Proposal — a board write asks the mirror loop for a tick

## Why

On 2026-11-13, 20:09 (+07), a project was created on the board of the homebox instance. The config file changed, the
store row appeared, and then the journal went quiet: no `mirror:` line, no dispatch, for four minutes. Restarting
`fleet-console.service` made it dispatch within seconds.

The loop was not dead — it was **idling**. `mirror.Cadence` doubles the wait after each quiet tick up to
`rig_lifecycle.idle_poll_max` (10 minutes by default, `internal/config/config.go:300`), and the last quiet tick
before the edit had already set an 8-minute wait (`mirror: cadence 8m0s (quiet 4)`). A board write changes what the
next tick would see, but nothing tells the loop that; only a verified forge webhook does
(`internal/web/forgehook.go:88`). So the operator's action waits out the backoff, and the silence reads as a dead
daemon — which is what happened.

The same gap covers every board write the next tick reads: an edit, a delete, a pause, and a **verdict** posted on
the item drawer (the tick captures it and merges on it, but only at the next scheduled poll).

## What changes

`internal/web`: the project create, edit, delete and pause routes, and the verdict route, call the existing
`mirror.Waker` after the write succeeds. The loop ticks immediately, reads the same config and store it always
reads, and keeps its cadence arithmetic untouched — a wake already resets the backoff to `tick_interval`.

## Losses, named

- **One extra tick per board write.** The waker coalesces (capacity 1), so a burst of writes costs one tick, not one
  per write.
- **A wake can land mid-tick.** It is a channel send that never blocks the handler; the loop selects on it between
  ticks, so a wake during a tick is served by the next one.

## Capabilities

`mirror-loop` — when the loop is asked to tick. No new event kind, no schema change, no new endpoint.

## Impact

`internal/web` (projects, verdict, fleet, app wiring) and one test harness field. The loop, cadence and act code are
unchanged.

## Out of scope

- **Watching `config.yaml` for hand edits.** An operator editing the file outside the board is not an HTTP write; a
  file watcher is a separate change with its own debounce rules.
- **Shortening the backoff.** The ceiling exists to stop a quiet box hammering the forge; the defect is that a real
  change did not count as busy, not the ceiling itself.
- **A restart button on the board.** Restarting to force a tick is the symptom this change removes.
