# Design — one task per epic in flight

## D1. "In flight" is a dispatch or an unmerged change request

A child holds its epic while either is true:

1. it has a live dispatch row (`ReasonLiveRow` already computes this set), or
2. its branch has an open, unmerged change request (the snapshot's `branches` map already carries it).

Why not the dispatch alone: a builder opens a PR and its dispatch ends within seconds, so the next child would start
while the previous work is still unmerged on a branch off `main`. That is the conflict this change exists to prevent —
siblings in an epic routinely touch the same package. The epic advances when the previous child **lands**, not when its
builder stops typing.

Why not "any branch at all": a closed or merged CR leaves a branch behind forever. Only an unmerged open CR counts, and
only while the item is still open. A branch whose item closed holds nothing.

## D2. Enforced in `Decide`, not in the rig

The rule belongs where seats are filled (`internal/decide`, the ready loop in `decide.go` around the parent checks and
the seat assignment at `plan.go:385`), because that is the one place that already knows every fact it needs: items with
their parents, live dispatch rows, branches, and free seats. Nothing new is read, nothing is written, and a decision is
one pure function of the snapshot — the property the rest of `Decide` has.

`Rejected:` enforcing in `act` before dispatch. It would duplicate the seat accounting and the skip ledger, and the plan
as printed would still say two items were dispatched.

## D3. Order is the item number

Among eligible siblings, the lowest number is taken. It is the order the epic was written in, it is stable across ticks,
and it needs no priority field. A project that wants another order reorders its issue numbers or edits the epic; the
daemon does not guess at intent.

## D4. The holding sibling is named

The skip is `ReasonEpicBusy = "epic_busy"` with detail `#<n> in flight` (or `#<n> in review` when the holder is a CR and
not a dispatch). This distinguishes "wait for the queue" from "wait for a human" — a distinction the board already
fails to make in other places (`why-the-fleet-is-waiting`).

## D5. One escape hatch, default on

`epic_serial: false` on a project restores today's behaviour. It exists because some epics are genuinely independent
lists (a sweep of unrelated chores), and because a rule with no off-switch gets worked around with `onhold` by hand —
which is exactly what happened today: five children of #424 carry a label a human had to remember to remove.

Default is `true`. An epic that was written as a plan is serialized; a project that knows better says so in its file.

## D6. What does not change
- `onhold` keeps its meaning: a child carrying it is skipped with `parent_onhold`/`onhold` as today.
- A sibling on `onhold` does not hold the epic: it is not in flight.
- The concurrency cap still applies; this rule only removes candidates, never adds seats.
- A child whose parent is not an open epic keeps its existing skip.

## Decisions (grilled 2026-11-14)

1. **A stalled holder keeps the epic.** A live dispatch whose seat goes silent is not released by this rule; the
   silent-dispatch question already tells a human, and auto-releasing risks two builders in one epic — the failure the
   change exists to prevent. *Carried by:* "One child of an epic is in flight at a time" (scenario: a silent holder).
2. **A draft or red change request still holds.** `decide.Branch` carries only `HasOpenCR`/`HasMergedCR`; draft and CI
   state are not in the snapshot and are not added. An open unmerged CR is unlanded work whatever its draft state.
   *Carried by:* the same requirement (scenario: a draft CR).
3. **Siblings in flight at deploy are grandfathered.** The rule refuses new dispatches; it never cancels a running one.
   #428 and #429 keep running when this lands. *Carried by:* the same requirement (scenario: two already in flight).
4. **Merged or closed both free the epic.** A closed unmerged CR is abandoned work: `HasOpenCR` goes false, the next
   sibling is eligible, and its dispatch creates a fresh branch. *Carried by:* the same requirement (scenario: a
   closed CR).
