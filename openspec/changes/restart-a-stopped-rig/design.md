# Design — a stopped rig is not a quiet rig

## D0. Up or down is the running seats, counted as running

The first fact to fix is the one the rule reads, not the one it remembers. `buildLifecycleRigs` counted seat *rows*,
and a stopped rig keeps its rows, so a quiet-stopped rig was permanently "up": the start arm never ran and the stop arm
fired every tick. Counting only `lifecycle == "running"` restores the accepted spec's own sentence — *the running seats
are the only fact about whether a rig is up* — and needs no memory, no new flag, and no new log line.

`Rejected:` remembering the previous tick's running count (`lastRunning`) as the primary repair. It would also have
masked this error: the memory would hold the wrong number, and it is a remembered flag, which the same spec forbids
("A remembered flag, a decision made by the previous tick … SHALL NOT be used"). It stays useful only as a fallback
for an unread roster (D1's subject), and only after the count is right.

## D1. "Unknown roster" must not mean "decide nothing" for the start half

`SeatsKnown=false` means this tick could not read the rig. For the **stop** half that is the right answer: stopping a fleet you cannot see is destructive. For the **start** half it is the wrong answer, because the read failing is one of the states a start repairs — and a stopped rig will never produce a successful read on its own.

So: when the roster is unknown and the last known state had zero running seats, the rig is treated as stopped-with-unknown-roster and the start is allowed, gated on demand (a fresh managed project with `Ready > 0`) exactly as today. The action carries the reason `roster unknown; N ready item(s)`, so the log says which arm fired.

`Rejected:` starting on every stale read. A stale read with no demand starts nothing, and a rig whose last known state had running seats is never started on unknown data — that could duplicate a fleet.

## D2. The last known seat count is remembered, not re-derived

The one piece of memory the lifecycle already keeps is `quiet` (per-rig streak). This change adds `lastRunning map[string]int`, updated only on ticks where `SeatsKnown` is true, and read only when it is false. Memory-only, same as `quiet`: a restart forgets it, and the worst case is one tick that decides "unknown" instead of "stopped" — a delay, never a wrong start against a live fleet.

`Rejected:` storing it in the database. It is a per-process fact about a read, not state the product owns; persisting it would make a stale row look authoritative after a restart.

## D3. Every rig decision is one line, including "none"

`DecideLifecycle` returns actions; today a rig with no action produces no line at all. The tick will log one line per rig it considered:

```text
rig-lifecycle: <rig> none — running=0 ready=0 seats_known=true
rig-lifecycle: <rig> start — roster unknown; 3 ready item(s)
rig-lifecycle: <rig> stop — quiet 12m3s
```

The line is assembled from the same facts the rule used (the `LifecycleRig` entry after `buildLifecycleRigs`), so it cannot drift from the decision. It is one line per rig per tick — the cadence is seconds to minutes, and the journal already carries a per-project `mirror:` and `act:` line per tick.

`Rejected:` logging only when the decision changes. "It changed at 16:04 and never again" is exactly the case where the operator needs to see the steady state that is wrong.

## D4. A plan skip is printed once per item and reason

`decide.Result.Skips` is not read by `act`. The tick will print one line per (item, reason) per head, reusing the existing notice ledger so a perpetually-skipped item does not print every tick:

```text
act-skip: fleet-console: #393 no_free_builder — every admitted seat is taken
```

This is the half of #402 that this change owns; the operator-facing reason surface (the board) stays with #402.

## D5. A start still costs one attempt per backoff window

The executor already records the attempt (`lifecycle.go:403,428-448`) and `daemon-down-start` proved the backoff. The new arm reuses it unchanged: one start, then backoff, then one more. A failed start is a `rig.started` event with the error, not a crash loop.
## D6. Who stopped the rig decides whether it may be started again

Once up-or-down is the running seats (D0), a stopped rig with demand is visible — but not every stopped rig is ours to
start. `rig down` and `rig seat stop` are the same daemon verbs the quiet rule uses, so the rig's own state cannot name
the author: a quiet-stopped rig and an operator-stopped rig are identical (`lifecycleState: stopped`, every row
`exited` with `intentionallyStopped: true`).

The app's own ledger can name it. The executor is the only writer of `rig.stopped` (detail `quiet <dur>`) and
`rig.started` (detail: the reason), so `store.QuietStoppedRigs` answers one question per rig: *is the newest lifecycle
event we wrote for it a stop?* Only a rig that answer names is started again on demand. A stopped rig with no such
record was stopped by a person, and starting it would overrule a decision they made on purpose.

`Rejected:` reading `intentionallyStopped` from the node rows. Both stop paths set it, so it cannot separate them.
`Rejected:` treating an unknown provenance as demand. The failure it would create — a rig the operator deliberately
drove down coming back minutes later — is the one this acceptance protects; the cost of being wrong the other way is a
rig that stays down until the next `rig start`, which is bounded and visible in the journal.

The daemon-down arm is exempt: nothing a person did takes a daemon down, and the accepted spec already starts the rig
on demand in that case. An unread ledger (`QuietStoppedRigs` errors) decides nothing this tick — the next tick asks
again — because failing toward a start is failing toward overriding the operator.
