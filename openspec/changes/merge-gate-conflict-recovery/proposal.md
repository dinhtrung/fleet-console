# Proposal — a head that cannot merge is never reviewed, and a refusal is never invented

## Why

Three facts about one refusal, measured on `2026-11-13` while T3 of epic #309 was in flight.

**1. GitHub's "not computed yet" is read as "not mergeable".** `ChangeRequests` refetches a pull request when the
list page leaves `mergeable` null (`internal/forge/github.go:317-341`), but reads only `mergeable` from that second
response: `MergeState` still comes from the list page, where it is `unknown` in the same window
(`github.go:341`). A pull request that GitHub has not finished scoring therefore arrives as
`ChangeRequest{Mergeable: false, MergeState: ""}`. The gate refuses it (`internal/act/merge.go:26-31`) with no forge
word, `blockedMerge` classes it as a conflict (`merge.go:55-62`), and `RaiseMergeRepair` puts a `human@kernel` row on
the queue (`tick.go:198-208`, `repair.go:42`) — once per `(item, head)`, so the bogus row outlives the read that
created it. The incident's own line proves the shape:

```
#312 merge waiting: refusing merge: change request #335 is not mergeable
```

No `(dirty)`, no `(cannot_be_merged)`: the forge never said "conflict", and the app invented a refusal anyway.

**2. A conflicting head is reviewed anyway.** PR #335 opened at `07:39:13Z` on base `a086fee`; the sibling task's
change (#336) merged at `07:41:48Z` and rewrote the same function. The pull request was `CONFLICTING` / `DIRTY` from
that moment, and still cost a full review that ended in `ACCEPT c11a6f0` with three green checks. `MergeConflict()`
(`internal/forge/forge.go:152`) exists and no planning path calls it: `decide` asks the operator to approve a
`decision.no_verdict` head (`internal/decide/plan.go:318-330`) and asks the reviewer for a `decision.stale` re-review
(`plan.go:331-340`) without ever looking at the base. The gate learns the truth only after the seat is spent.

**3. The repair decision is never reconciled.** The row is keyed by `(item, head)` and raises once
(`repair.go:42`). Nothing resolves it when the premise disappears: not the item closing, not the change request
closing, not a rebased head merging. A rebase is a new key, so the queue can collect one dead row per head, and the
operator's only exit is answering a question whose facts are already stale.

## What changes

One change set, three ordered tasks, one behaviour each:

1. **Mergeability has three states.** The adapter carries `mergeable_state` from the read that computed it, and the
   tick treats an uncomputed answer as waiting — no refusal, no repair row, no page.
2. **A conflicting head raises the repair, not a review or an approval question.** `MergeConflict()` becomes a
   planning input: no `decision.no_verdict`, no `decision.stale` while the base conflicts.
3. **The repair row is reconciled.** At most one open row per item; a moved head is a note on that row; the row is
   resolved when its premise disappears, with the merged head named.

The code tasks are #338, #339 and #340. The change set is authored before them, as epic #341 requires.

## Losses, named

- **An uncomputed mergeability delays a merge by one tick.** A pull request that GitHub could score instantly now
  waits for the next tick if the read lands in the `unknown` window. That is the price of never inventing a refusal;
  the wait is reported, not silent.
- **A dirty head loses its review.** A reviewer is no longer asked to review a head that cannot merge. When the
  conflict is repaired the head changes, so the review would have been invalidated anyway: nothing real is lost.
- **The reconciliation reads the queue.** It calls `QueueShow` for rows the ledger names, on the tick that sees the
  premise gone — one read per (item, resolution), not per tick.

## Capabilities

`act-merge` — the merge gate's three-state mergeability, plan-time conflict routing, and the repair row's lifecycle.
No new capability, no schema change, no new event kind.

## Impact

`internal/forge` (GitHub adapter, `ChangeRequest`), `internal/act` (tick, merge, repair), `internal/decide` (plan).
Frontend untouched. Docs: `docs/fleet-console.md`'s merge-repair paragraph gains the reconciliation sentence.

## Out of scope

- **Auto-rebase through the forge API.** GitHub's `PUT /pulls/{n}/update-branch` and GitLab's rebase endpoint change
  the head, which invalidates the verdict; the re-review path already covers that. It is a separate change because it
  needs a write scope the merge gate does not have today.
- **The rig's builder→reviewer handoff.** The app cannot stop a seat the rig already dispatched; this change stops
  the app from *asking* for a review it knows is wasted. The handoff rule belongs to the rig spec.
- **A task-ordering rule enforced by code.** It lives in `tasks.md §0` and the epic body: sibling tasks that touch
  one function land in order. #335's conflict is the evidence, not the deliverable.
