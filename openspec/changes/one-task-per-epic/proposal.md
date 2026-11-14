# Proposal — one task per epic in flight

## Why

At 00:36 on 2026-11-14 the daemon dispatched **#425 and #426 five seconds apart** (dispatches 381 and 382) to two
different seats of the same rig. Both are children of epic #424; #425 touches `internal/rig` and #426 touches
`internal/decide`, and the epic's remaining three tasks sat on `onhold` only because a human had typed it there.

That is not an accident of this epic. Earlier the same day #413 and #414, written in parallel against `internal/act`,
each added a helper named `supersededFixture`; both merged, and `main` stopped compiling until #423 renamed one. The
merge gate catches a broken product after the fact; it does not stop two builders from writing the same function.

An epic is a plan whose parts share a design. Running its parts in parallel asks several builders to guess the same
decisions at once, and the cost is paid at merge time as a semantic conflict that no test names.

## What this changes

- **At most one child of an epic is in flight.** A second child is not dispatched while a sibling holds a live dispatch
  or an unmerged change request.
- **The next child starts when the previous lands**, in ascending number order — the order the epic was written in.
- **The skip says who holds it**: `epic_busy: #425 in flight`, once, through the line #411 added.

## Scope

`internal/decide` (the rule and a new reason), `internal/config` (an opt-out), tests, `docs/fleet-console.md`. No change
to the rig adapter, the merge gate, or the retry/backoff paths.

## Non-goals

- Not a project-wide cap: independent issues still run in parallel, up to `concurrency_cap`.
- Not dependency inference. Two children are serialized because they are siblings, not because the spec guesses which
  one needs the other.
- Not a review gate. A sibling with an open change request blocks the epic's next child, but merging stays the gate's
  decision on a verdict.
