# Design — idle stop, demand start, slow poll

## D1. Demand is what the plan would do, not what the forge holds

The start trigger cannot be "an open issue exists": every open issue may be blocked, held, already dispatched or
already carrying a change request, and starting a rig for it would launch three seats to do nothing. The plan
already answers exactly the right question — `decide.Decide` walks the ready items and fills `Result.Dispatches`
up to `limit`, which is `len(free)` capped by `ConcurrencyCap`.

So the plan gains one integer, `Result.Ready` (`len(ready)` before the limit), and the start needs
`Ready > 0 && Free == 0` on a rig with zero running seats. That is precisely "there is work, and no seat to take
it".

Rejected: parsing the `no_free_builder` skip reasons. That reason also fires when every seat is busy at the cap,
so the parser has to re-derive the distinction the plan already had. Rejected: reading `rig.yaml` to learn the
seat names. It is a second source of truth that can be stale, and preflight already owns the "does the spec
exist" question.

The second arm of the trigger is the rig's own queue: a non-terminal row whose destination is `<seat>@<rig>` and
which is not blocked on a human. That covers a row resolved while the rig was stopped, and any row created by
hand during a bootstrap — neither produces a dispatch intent, so the plan alone would never wake the rig.

## D2. The running seats are the only fact about up or down

`SeatsForRig(rig)` is read on every tick, and `len(seats) == 0` means stopped. Nothing is remembered, nothing is
stored in the DB, and the spec on disk is not consulted. This is what makes three operator actions safe: a manual
`rig up` is never duplicated, a manual `rig down` stands until work needs the rig, and a service restart cannot
act on a decision the previous process made about a rig that has since changed.

Rejected: a `rig_lifecycle` table holding `state` and `since`. It buys nothing the roster does not already say,
and it introduces the one thing a control plane must not have — a belief about a process it does not own.

## D3. Stop needs four facts, and a stale read vetoes all of them

A stop is only proposed for rig R when all of these hold on the same tick:

1. Every project bound to R has a **fresh** forge read (no `ForgeStale`, no `ForgeError`).
2. No project bound to R has demand (`Ready > 0`) or a non-terminal, unblocked row.
3. No non-terminal, unblocked row is addressed to a seat on R.
4. R has been quiet continuously for `idle_after`, measured from the first quiet tick.

Fact 1 is the important one and the reason this is not simply "the board is empty": a failed forge read produces
the same empty item list as a closed repository, and stopping the rig on a broken read is the failure mode that
teaches an operator to distrust the automation. The mirror already marks such a project `stale`; the lifecycle
reads that flag and refuses.

A row **blocked on the operator** (`blocked_on` is a human) does not count as work: no seat can act on it. It also
does not lose anything — the row lives in the daemon's queue, not in the seat's process — and the moment it is
answered it becomes claimable, which is the second arm of D1's start trigger.

## D4. Stop is `rig down <name>`, never `--delete`

`--delete` removes the rig record, its snapshots and the history the next `rig up` and the board read. The idle
stop is a power-saving action, not a teardown. Queue rows and specs survive either way; the deletion is the one
part that does not.

## D5. Start happens before the dispatch writes anything

`internal/act/actions.go` already creates the worktree and the queue row before recording the dispatch — a known
crash window. Adding "and the seats are not up yet" to that window makes a recoverable state into a confusing
one (worktree and row, nothing to claim, and a `no free builder` plan that never retries).

So the tick applies `rig up` first, and a failed launch **skips the dispatch with one named reason** (`act-skip:
<key>: rig <rig> up: <err>`), creating no worktree and no row. The intent survives in the forge, so the next tick
retries it naturally.

Rejected: create the row anyway and let the seat pick it up when the rig comes up. It needs a second retry path,
leaves orphan worktrees, and hides the failure from the one log line that would explain it.

## D6. The quiet window is memory-only, and it starts on the first quiet tick

`map[rig]time.Time` plus `map[rig]bookkeeping` in the running process. A restart resets both, which is the
conservative direction: after a deploy the rig gets a fresh `idle_after` before it can be stopped again. There is
no persistence requirement because the only thing a stale window could do is stop a rig sooner than intended.

## D7. Backoff is a property of the loop, fed by the tick's own read

`mirror.Loop` currently takes a fixed `interval` and a `jitter`. It gains a `Cadence` with
`Next(quiet bool) time.Duration`: doubling from `tick_interval`, capped at `rig_lifecycle.idle_poll_max`, and
reset to `tick_interval` on the first busy tick. The verdict comes from the read the tick already performed:
busy when any project has an open item, an open change request, a non-terminal row, or a stale/failed read.

Busy is deliberately broader than "has an intent": an item blocked on another item, an item on hold, or an open
change request awaiting a verdict are all reasons to keep watching at the normal cadence, and none of them
produces a dispatch.

Rejected: a per-project cadence. The mirror reads every project in one pass; a per-project wait would mean either
N loops or one loop spending its budget on the busiest interval.

## D8. One log line and one audit event, no phone message

Housekeeping is not work and not a decision, so it must not cost the operator attention — that is the rule the
`quiet-notify` change already set. The start and the stop each write one line
(`rig-lifecycle: fleet-console stopped — quiet 10m0s`) and one audit event, which is what the board and the audit
log read. Failures are logged once per change (the `mirror.note` pattern), not once per tick, so a permanently
broken `rig up` does not fill the journal.

## D9. Opt-in, with a per-project escape

`rig_lifecycle.enabled` defaults to false: an upgraded box must not start stopping rigs because a binary changed.
The block also carries `idle_after` (default 10m) and `idle_poll_max` (default 10m). A project that must stay
warm — a demo, a rig someone is watching — sets `lifecycle: off` and is excluded from both the stop and the
cadence verdict for its rigs.

`--print-config` shows the block and each project's effective value with its source, the way every other setting
is shown; a `idle_poll_max` below `tick_interval` is refused at load with both values named rather than silently
clamped, because a clamp would make the operator's number a lie.
