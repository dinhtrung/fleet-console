# Design — a head that cannot merge is never reviewed, and a refusal is never invented

## D1. Mergeability has three states, and the adapter must say which

Today `ChangeRequest.Mergeable bool` carries two states and conflates two different `false`s: *the forge says it
cannot merge* and *the forge has not answered yet*. Each forge names its own pending state — GitHub
`mergeable: null` with `mergeable_state: "unknown"`, GitLab `detailed_merge_status: "checking"` — so the adapter is
the only layer that can tell them apart.

- `internal/forge/github.go`: the single-PR refetch already exists for `mergeable == null`; extend its decode to
  include `mergeable_state` and carry **that** value instead of the list page's.
- `ChangeRequest` gains `MergeUnknown() bool`: true when the forge's word is one of `""`, `unknown`, `checking`.
  Empty stays unknown on purpose — that is what today's refetch produces, and a missing word is not a refusal.
- `MergeConflict()` is unchanged: `dirty` (GitHub), `cannot_be_merged` (GitLab).

`Rejected:` treating unknown as mergeable. A blind merge is worse than a tick of latency.
`Rejected:` retrying the forge read inside one tick. GitHub computes mergeability asynchronously; a spin loop turns a
one-tick delay into a blocked loop, and the tick already has a next iteration.

## D2. Waiting is a state the tick reports, not an error it records

The unknown case is handled in the tick, before `Merge`/`MergeAdopted` is called, so no error type can be mistaken
for a refusal by `blockedMerge` or by `errors.As(err, &NotMergeableError)`:

```go
if request.MergeUnknown() {
    report.Skipped = append(report.Skipped,
        fmt.Sprintf("#%d merge waiting: the forge has not computed mergeability", item))
    continue
}
```

No event, no row, no page. The line is grep-able and disappears on the first tick that gets a computed answer.

`Rejected:` a `NotMergeableError{Reason: "unknown"}`. Every consumer of that type treats it as a dead end —
`RaiseMergeRepair`, `blockedMerge`, the operator's page — and the classification would have to learn a second,
opposite meaning for one reason string.

`Rejected:` swallowing the case silently. Silence is what makes a real stall hard to see; one waiting line per tick
is the same noise level a pending check already produces.

## D3. Conflict is a planning input, not a merge-time discovery

`MergeConflict()` is a fact on the change request the plan already holds. The plan routes on it before it asks
anyone for anything:

| Situation | Today | After |
|---|---|---|
| dirty head, no verdict | `decision.no_verdict` to the operator | `decision.merge_repair` |
| dirty head, verdict on an older sha | `decision.stale` to the reviewer | `decision.merge_repair` |
| dirty head, verdict on the head | nothing (merge gate refuses later) | `decision.merge_repair` |
| mergeability unknown | `NotMergeableError`, repair row | waiting line, no row |

The repair row keeps its existing text and brief (`repair.go:52-63`): the two options are still rework or defer, and
the branch and forge word are still named. Only who asks, and when, changes.

`Rejected:` skipping the merge only (`if request.MergeConflict() { return NotMergeableError }` earlier in `Merge`).
That moves the discovery by three lines and still spends the review, which is the expensive half.

Named honestly: the app cannot stop a review the rig has already handed to a seat. This design stops the app from
**asking** for a wasted one; the handoff rule is the rig spec's business.

## D4. One open repair row per item, reconciled with its premise

The raise is keyed `(item, head)` and the event detail already carries `row <id>` (`repair.go:42-78`). That ledger is
enough to reconcile without new state:

1. **Before raising**, read the newest `decision.merge_repair` event for the subject `#<item>`, `QueueShow` its row,
   and treat a non-terminal row as the open one. If it exists and names another head, append a note naming the new
   head (`QueueUpdate --note`) and write the event for the new head; do **not** create a second row.
2. **On every tick**, for each item with a non-terminal repair row, resolve the row when the premise is gone: the item
   is closed, the change request is closed/merged, or the current head is green and carries an ACCEPT. Resolution
   goes through `ResolveRow` (the same path a human answer uses), with a note naming the fact that ended it.
3. **If the ledger's row is terminal**, the next conflict at a new head raises exactly one new row — the `(item, head)`
   key still prevents a re-raise at the same head.

`Rejected:` a `repair_open` column in the store. The event ledger already names the row and the queue owns the row's
state; a second copy is the state duplication this repo refuses.

`Rejected:` resolving the row when the head merely moves. A moved head with an open conflict still needs the
decision; only the premise ending closes it.

## D5. What a reviewer of this change should check first

- `forge.ChangeRequest.MergeState` is populated from the response that computed `mergeable`, on both adapters.
- No path reaches `RaiseMergeRepair` with `MergeState` in `{"", "unknown", "checking"}`.
- No path reaches `RaiseDecision` (`decision.no_verdict`, `decision.stale`) while `MergeConflict()` is true.
- Reconciliation resolves through `ResolveRow`, so a human answer and an automatic resolution cannot both win.

## Tests that prove the promises

| Promise | Test |
|---|---|
| unknown is waiting, not a refusal | `TestUnknownMergeabilityWaitsWithoutRepair` |
| the refetch's word wins | `TestGitHubMergeStateComesFromTheComputingRead` |
| no approval asked for a dirty head | `TestPlanRaisesRepairInsteadOfNoVerdictOnConflict` |
| no re-review asked for a dirty head | `TestPlanRaisesRepairInsteadOfStaleOnConflict` |
| one row per item, noted per head | `TestMergeRepairNotesANewHeadOnTheOpenRow` |
| the row dies with its premise | `TestMergeRepairResolvesWhenTheItemMerges` |
| a resolved row does not block a new one | `TestMergeRepairRaisesAgainAfterResolution` |
