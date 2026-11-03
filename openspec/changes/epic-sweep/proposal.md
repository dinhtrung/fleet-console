# Proposal — sweep open epics on every tick, not only when a merge ends one

## Why

The rollup closes an epic only inside a merge path. `rollupAfterMerge` is called for the item an applied tick has
just merged, so an epic closes only when the *last* sub-issue happens to be closed by a merge processed in that same
run. An epic whose last sub-issue closes any other way stays open forever:

- an operator closes the last sub-issue by hand;
- a sub-issue is closed as dropped, duplicate or superseded;
- the merge landed in an earlier tick, or the rollup failed and the retry never comes because there is no later
  merge to hang it on.

The board then keeps the epic in its open set, the fan-out never retires, and a human closes it by hand — the case
that keeps coming back. Epic #122 records the problem and its two sub-issues: this change (#123) specifies the rule
and delivers the decision, and #124 lands the write.

## What changes

- A **sweep step**: every applied tick asks, for each open epic of the project, whether all of its sub-issues are
  closed, and closes it when they are — with no merge involved.
- The decision is a **pure function**, `Sweep(epic, items) Decision`, beside `Rollup` in `internal/act`: the epic's
  own row and a read of its sub-issues in, close-or-skip with a reason out. No clock, no forge, no store.
- The rule is the rollup's rule applied to a candidate the tick observed rather than an item it merged: zero
  sub-issues never closes, one open sub-issue keeps the epic open **without a skip line on every tick**, and an
  epic already closed is silent.
- The close stays attributable: the sweep's event and comment name the sweep as the source, so an operator reading
  the audit log tells a sweep close from a merge close.
- `Rollup` and `rollupAfterMerge` do not change. The merge path keeps its behaviour, and the sweep does not replace
  it: a tick that merged still rolls up through the path that already ships.

## Capabilities

### New Capabilities

- `epic-sweep` — the sweep rule (which epics it considers, the zero-children guard, the open-child silence, the
  attribution of the close, the failure path) as a behaviour contract for the tick that runs it.

### Modified Capabilities

None. The merge-time rollup keeps its own delta in `epic-rollup`, untouched.
