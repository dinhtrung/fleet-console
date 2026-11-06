# Proposal — a rig runs only while it has work

## Why

The rig is a process the operator starts by hand and then leaves running. Three omp seats at their prompts hold
sessions, memory and the tmux stack for as long as the box is up, and nothing in the app ever stops them: on this
box the `fleet-console` rig has been `status: stopped` for 10h51m while the board still had work to plan.

The opposite end fails too, and worse. `mirror.BuildSnapshot` fills `Snapshot.Builders` from the **running** seats
(`internal/mirror/mirror.go:402`), so with zero seats every ready item is skipped `no free builder` and the plan
produces no intent at all — a stopped rig cannot be woken by the issue that needs it, because the app never sees
the demand. The fleet therefore has one failure at each end: an idle rig that never sleeps, and a sleeping rig
that nothing can wake.

The poll is the second half of the same problem. A tick costs one forge call per item plus one per change
request; the box hit load 30 at nine open pull requests, and a repository with nothing open is still polled at
`tick_interval` — 15s by default. "Nothing happened" is not worth that many calls an hour.

## What changes

- **Idle stop.** A rig whose every project has been quiet for `rig_lifecycle.idle_after` is stopped with
  `rig down <name>` (never `--delete`, so spec, snapshots and queue rows survive).
- **Demand start.** A rig with no running seat that a project needs is launched with `rig up <spec>` **before** the
  dispatch creates its worktree and queue row. "Needs" is the plan's ready count, or a claimable queue row whose
  destination is one of that rig's seats — so both a new issue and a resolved row can wake it.
- **Backoff.** The tick loop doubles its wait after each tick that finds nothing, capped at
  `rig_lifecycle.idle_poll_max`, and returns to `tick_interval` on the first tick that finds anything.
- **Visible, not announced.** Every action is one log line and one audit event (`rig.started`, `rig.stopped`); no
  Telegram notice. Housekeeping is not work and not a decision (the `quiet-notify` rule).
- **Opt-in.** `rig_lifecycle.enabled` defaults to false; a project can opt out with `lifecycle: off`.

## Losses, named

- **Detection latency equals the poll wait.** With the default ceiling of 10m, a new issue can wait up to ten
  minutes before the mirror sees it, and then `rig up` costs the seat launch time on top. That is the price of the
  quiet poll; the ceiling is the one number that trades quota for latency.
- **A stop costs the seats their sessions.** Anything a seat had not pushed is gone. Two guards bound it: the
  window only opens after `idle_after` of quiet, and a rig with a non-terminal, unblocked row is never stopped.
- **A restart forgets the quiet window.** The streak is in memory by design, so a deploy delays the next stop
  instead of causing one. The cost is that a box which restarts every few minutes never stops its rigs.

## Capabilities

### New Capabilities

- `rig-lifecycle`: idle stop, demand start, observed-state rule, stop guards, log/audit, opt-in configuration.
- `poll-cadence`: the doubling wait, its ceiling, its reset, and the one place both are configured.

### Modified Capabilities

None. `decide` gains a `Ready` count so the lifecycle can tell "no seat to take it" from "nothing to take", but
the dispatch rules themselves are unchanged.

## Impact

- `internal/config/config.go` — `rig_lifecycle` block, per-project `lifecycle`, defaults, `--print-config`.
- `internal/decide/decide.go` — `Result.Ready` (items that would dispatch with an unbounded roster).
- `internal/act/lifecycle.go` (new) — the pure decision: facts in, actions out.
- `internal/act/tick.go`, `internal/act/actions.go` — start before the dispatch writes, stop after the tick.
- `internal/rig/adapter.go` — `Up`/`Down` are already there; the tick needs them behind one small interface.
- `internal/mirror/mirror.go`, `cmd/fleet-console/main.go` — the cadence and the busy verdict.
- `PRODUCT.md`, `docs/my-stack.md` — the operator-facing statement.
- No forge, store or web change. No new environment variable. No Telegram surface.
