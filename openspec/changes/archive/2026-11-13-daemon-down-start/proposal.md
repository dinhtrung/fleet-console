# Proposal — a rig that is down is started when work arrives

## Why

The lifecycle feature stops an idle rig and starts a stopped one — but only when the rig daemon answers. When the
daemon itself is down (a manual `rig daemon stop`, a host reboot, a crash), the app decides nothing and the fleet
stays down until a human runs `rig up`:

- `cmd/fleet-console/main.go:818` passes `seatsKnown = !read.RigStale`, and `read.RigStale` is set whenever the rig
  read fails at all (`internal/mirror/mirror.go:121`).
- `DecideLifecycle` refuses to act on an unknown roster (`internal/act/lifecycle.go:229`):
  *"No roster: the tick can neither launch (which could duplicate a fleet that is already up) nor stop."*
- Every other piece of the start path already exists and is unused in this case: the spec-exists guard, the
  `rig.started` event, the demand sum over managed projects, and `Adapter.Up` (`rig up <spec> --non-interruptive`).

So a box that rebooted overnight wakes with ready issues on the board and a dead fleet, and the app that stops rigs
to save quota is the one reason nothing runs. Measured on homebox: `rig_lifecycle` is enabled and the tsim
project is managed, so this is the live configuration, not a hypothetical.

The reason the guard exists is real — an unreadable roster could mean a fleet that is up — but it conflates two
different facts. A daemon that answers "Daemon not running. This command needs a running daemon." is not an
unreadable roster: nothing can be running on a daemon that does not exist.

## What changes

- `rig` classifies that answer as `ErrDaemonDown` (a typed error), instead of a generic wrapped failure.
- The mirror carries `RigDown` next to `RigStale`, and the tick passes it to the lifecycle rule.
- `DecideLifecycle` treats a down daemon as "no seat is running, roster known" **for the start half only**: the stop
  half keeps refusing, because `rig down` against a dead daemon is a failure, not an action.
- A start attempt is limited to one per rig per backoff window (`StartBackoff = 5m`, memory-only, like the quiet
  streak), so a broken spec cannot launch a `rig up` every 15 seconds.
- `Adapter.Up` gets its own timeout (`StartTimeout = 5m`): the CLI's default 20 s bounds a read against a local
  daemon, and a cold start boots a daemon plus every seat.

## Losses, named

- **The app can now start a daemon it did not start.** On a box where the operator deliberately stopped the daemon,
  a ready issue starts it again. That is the requested behaviour, and `rig_lifecycle.enabled` plus the per-project
  `lifecycle: off` veto are the switches that keep it honest.
- **A wrong spec costs one failed launch per five minutes**, logged, until someone fixes it.
- **The first tick after a start still dispatches nothing**: seats need time to boot, so the fleet's first work
  happens a tick or two later. Starting early is what buys that time.

## Capabilities

### New Capabilities

- `rig-lifecycle` — the start half learns one more fact: a daemon that is not running means every rig on it is down.

## Impact

- `internal/rig`: `ErrDaemonDown`, `StartTimeout`, classification in the adapter's error path.
- `internal/mirror` + `internal/store`: `RigDown` on the tick result and the mirror status row.
- `internal/act/lifecycle.go`: the rule, the backoff, the start reason.
- `cmd/fleet-console/main.go`: pass the fact through; no new config key.
- `docs/fleet-console.md`: what a down daemon means for the lifecycle rule.

## Out of scope

- Restarting a *daemon that is up but wedged* (the read times out, and the app still decides nothing).
- Starting a rig whose spec does not exist (refused, as today).
- Any change to the stop half, the cadence, or the notification path.
