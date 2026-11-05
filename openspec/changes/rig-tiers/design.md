# Design — filling tiers, not a flat list

## D1. A union decode keeps the old file valid

`Rigs []RigRef` with a custom `UnmarshalYAML` that accepts a scalar (→ `{Name: s, Tier: 0, Spill: on-full}`) or a
mapping (`name`, `tier`, `spill`). The scalar form is what every existing config has, and making it mean exactly
today's behaviour is what lets this ship without a migration.

Defaults: `tier: 0`, `spill: on-full`. `spill` is only consulted for a tier greater than the first entry's tier;
an entry in tier 0 is never "spilled into" — it is used whenever it has a free seat.

Validation: a duplicate `name` inside one project is an error (two entries pointing at one rig would double-count
its seats); `spill` outside `never|on-full|on-unavailable` is an error naming the value; the existing rule that one
rig belongs to one project stays — a standby rig is a rig of *this* project, not a shared one.

## D2. Seats carry their rig, so the plan can group them

`Snapshot.Builders` holds session names of the form `<seat>@<rig>` (`dev-build-1@fleet-console`). `decide` splits
on the last `@` and groups the free ones by rig, then walks the project's `Rigs` in declaration order:
tier 0's free seats, then the next tier when the trigger holds, and so on.

A seat whose rig is not in the project's list is ignored — the registry is the authority on what a project may use,
and today's flat list already relies on that (the mirror reads seats fleet-wide).

Rejected: a second `BuildersByRig` field on the snapshot, filled by the mirror. It would duplicate a parse the
plan can do in one line, and the two could disagree — the failure mode is dispatching to a seat on a rig the
project does not list, which is exactly what this design refuses.

## D3. The trigger is a predicate over the same facts the plan already has

- `on-full`: every free seat in every earlier tier is taken, by the concurrency cap as well as by live rows — the
  cap is a reason the earlier tier cannot take work, so it counts.
- `on-unavailable`: an earlier-tier rig has zero running seats, or its queue read failed (the mirror already
  records `RigError`/`RigStale` for the fleet-wide read; a per-rig zero-seat read is enough here).
- `never`: the tier is only used when an earlier tier has no entry at all, i.e. it behaves like a later tier that
  cannot be reached.

`freeBuilders` and `busyBuilders` already compute the first two facts; this design only adds "per tier".

## D4. One dispatch, one seat, one rig

`Result.Dispatches` already carries `{Number, Seat}`. A seat names its rig, so `Actions.Dispatch` needs no change:
the row's `destination` is `<seat>@<rig>` and the rig's own daemon routes it. What must not change: an item is
dispatched once per tick, and the concurrency cap stays per project (it is a cap on the project's work in flight,
not on a rig).

## D5. The refusal is named where the operator looks

When a ready item cannot be dispatched because no tier qualifies, the skip carries the tier and the trigger:
`no_free_builder tier 1 (spill=on-unavailable: rig standby has 0 seats)`. The journal already prints every skip
verbatim; without the tier the operator cannot tell a busy fleet from a misconfigured spill.

## D6. Back-compat is a test, not a promise

A table test loads a config with `rigs: [a, b]` and asserts the resolved entries are both tier 0 and that the plan
for two free seats dispatches in listed order — byte-for-byte the behaviour of `main` today. That test is the
migration guarantee.
