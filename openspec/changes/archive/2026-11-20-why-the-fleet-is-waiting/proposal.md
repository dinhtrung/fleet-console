# Proposal — why the fleet is waiting

## Why

Twice today the fleet stopped and the control plane said nothing about the reason:

1. **A seat waiting on a human.** Restoring `fleet-console-rig` returned
   `dev.review attention_required — Restore startup requires attention: Ha…` and the queue stayed blocked until the
   operator answered a permission prompt in the rig's UI. Nothing on the board showed that a seat was waiting; the
   only way to find it was `rig status` by hand. The app already knows the state — `attention_required` counts as *not
   running* in the lifecycle rule (`internal/act/lifecycle.go:155`, test at `lifecycle_test.go:289`) — it just never
   shows it.
2. **An item that is ready and not moving.** After #411 a skipped item writes `act-skip:` to the journal, which is
   good for an operator reading logs and useless for one looking at the board. The reason lives in one tick's memory
   and is gone by the next.

The rig also reports the data the board needs and the app ignores it: `fleet-console -compat` on homebox warns
`unknown key "needsAttention"`, `"arbitratedNeedsInput"`, `"needsInputEvidence" in rig 0.7.13`.

## What this adds

- **A seat that waits on a human is a visible obligation**: which seat, which rig, the question it is waiting on, and
  how long — on the project page, with the same weight as a decision row.
- **The board keeps the last plan reason per item**, so "no free builder", "live row", "branch open" survive the tick
  that produced them.
- **The capacity line tells the truth**: "2 builders, one waiting on a human" is a different problem from "no builders".

## Non-goals

- No approve button. Answering a permission prompt is a human act in the rig's own surface; the board links to the
  seat and records that the obligation exists, it does not answer for the person.
- No polling of the rig beyond the read the tick already does.
- No change to the lifecycle rule: an `attention_required` seat still counts as not running (that is correct — a seat
  waiting on a human is not doing work).

## Impact

`internal/rig` (decode the three keys the rig already sends), `internal/act` (one notice per question, resolved when
answered), `internal/web` (a "waiting" panel and the last-reason column), `internal/store` (the plan's skips survive a
tick), `docs/fleet-console.md`. No schema change beyond one table or one event kind, decided in the design.
