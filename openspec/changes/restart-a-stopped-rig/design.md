# Design — a stopped rig is not a quiet rig

## D1. "Unknown roster" must not mean "decide nothing" for the start half

`SeatsKnown=false` means this tick could not read the rig. For the **stop** half that is the right answer: stopping a fleet you cannot see is destructive. For the **start** half it is the wrong answer, because the read failing is one of the states a start repairs — and a stopped rig will never produce a successful read on its own.

So: when the roster is unknown and the last known state had zero running seats, the rig is treated as stopped-with-unknown-roster and the start is allowed, gated on demand (a fresh managed project with `Ready > 0`) exactly as today. The action carries the reason `roster unknown; N ready item(s)`, so the log says which arm fired.

`Rejected:` starting on every stale read. A stale read with no demand starts nothing, and a rig whose last known state had running seats is never started on unknown data — that could duplicate a fleet.

## D2. The last known seat count is remembered, not re-derived

The one piece of memory the lifecycle already keeps is `quiet` (per-rig streak). This change adds `lastRunning map[string]int`, updated only on ticks where `SeatsKnown` is true, and read only when it is false. Memory-only, same as `quiet`: a restart forgets it, and the worst case is one tick that decides "unknown" instead of "stopped" — a delay, never a wrong start against a live fleet.

`Rejected:` storing it in the database. It is a per-process fact about a read, not state the product owns; persisting it would make a stale row look authoritative after a restart.

## D3. Every rig decision is one line, including "none"

`DecideLifecycle` returns actions; today a rig with no action produces no line at all. The tick will log one line per rig it considered:

```
rig-lifecycle: <rig> none — running=0 ready=0 seats_known=true
rig-lifecycle: <rig> start — roster unknown; 3 ready item(s)
rig-lifecycle: <rig> stop — quiet 12m3s
```

The line is assembled from the same facts the rule used (the `LifecycleRig` entry after `buildLifecycleRigs`), so it cannot drift from the decision. It is one line per rig per tick — the cadence is seconds to minutes, and the journal already carries a per-project `mirror:` and `act:` line per tick.

`Rejected:` logging only when the decision changes. "It changed at 16:04 and never again" is exactly the case where the operator needs to see the steady state that is wrong.

## D4. A plan skip is printed once per item and reason

`decide.Result.Skips` is not read by `act`. The tick will print one line per (item, reason) per head, reusing the existing notice ledger so a perpetually-skipped item does not print every tick:

```
act-skip: fleet-console: #393 no_free_builder — every admitted seat is taken
```

This is the half of #402 that this change owns; the operator-facing reason surface (the board) stays with #402.

## D5. A start still costs one attempt per backoff window

The executor already records the attempt (`lifecycle.go:403,428-448`) and `daemon-down-start` proved the backoff. The new arm reuses it unchanged: one start, then backoff, then one more. A failed start is a `rig.started` event with the error, not a crash loop.
