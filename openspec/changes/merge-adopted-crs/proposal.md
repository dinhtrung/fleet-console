# Proposal — merge a change request the app did not dispatch

## Why

The merge gate works, and it is running: every tick it re-reads the open change requests, captures a verdict, and
merges when the four facts line up. But it only knows an item by its **dispatch row** — `act.Tick` builds
`latest[item]` from the store's `dispatch` table and skips everything else:

```
act: thuocla dispatches=0 verdicts=0 merges=0 skipped=1
act-skip: thuocla: #37: change request #45 closes no dispatch
```

PR #45 was `Closes #37`, green on every check, `MERGEABLE`, and carried `ACCEPT 2519342d…` from the reviewer. It sat
unmerged with that skip repeating every tick, and because the verdict capture sits **after** the dispatch lookup the
ACCEPT was never even recorded. An operator had to mark it ready and squash it by hand.

Work reaches an item without an app dispatch whenever anything else writes the queue: a manual
`rig queue create` during an incident, the shell dispatchers, another orchestrator, or a builder that opened its own
row. That is not an edge case — it is how the owner usually works — and today every such pull request is
permanently unmergeable by the app while the log claims nothing is wrong ("closes no dispatch" reads like a
non-event).

## What changes

- The watchdog **adopts** a change request that closes an item with no dispatch row, and merges it under the same
  four facts it already demands, plus three guards that make adoption safe:
  1. the body says `Closes #<item>` (`forge.ParseCloses`), not a title reference;
  2. the head sha carries an `ACCEPT` verdict — captured from the change request notes, so adoption can never
     invent a verdict;
  3. checks are green and the change request is mergeable.
- **Guards against adopting a duplicate**: adoption is refused when a live dispatch exists for the same item on a
  different branch (that is the duplicate-branch case, and the human must pick), and refused when the branch does not
  start with `feature/<item>-`. Both refusals are named in the skip line with the change request number, so a stuck
  pull request always has one readable reason.
- Adoption writes its own ledger trail: `act/change_request.adopted` with the change request number and head sha,
  then the ordinary `merge` event. No fabricated `dispatch` row: a dispatch records a seat, a worktree and a brief
  the app never created, and inventing one would corrupt every later read of "who is working on what".
- The **verdict is captured even when the merge is refused**, including the adopted case. An ACCEPT that exists in
  the forge but not in the ledger is the reason this hole was invisible for hours.
- `Finalize` already closes the item and clears the marker; it must stay correct when it is called with no dispatch.

## Losses, named

- The app will now merge pull requests it did not create. That is the point, but it means a stray `Closes #<item>` on
  a hand-made PR is enough to be merged once a reviewer ACCEPTs it at that head. The guards (branch prefix, item open,
  verdict at head, green) are what keep this from being a foot-gun; there is no additional "trust" bit, because such a
  bit would be set by the same person who wants the merge.

## Capabilities

### Modified Capabilities

- `act-merge`: adds the adoption path and its two refusals; the dispatched path and the four facts are unchanged.

## Impact

- `internal/act/tick.go` — the watchdog loop: adoption branch, verdict capture before the dispatch lookup, skip text.
- `internal/act/merge.go` — `MergeAdopted` (or a dispatch-free arm of `Merge`), `Finalize` with no dispatch,
  `change_request.adopted` event.
- `internal/store` — reading a verdict for a change request that has no dispatch row; the `verdict` table keeps
  `dispatch_id` nullable or gains an adoption key.
- `internal/act/*_test.go` — unit + one end-to-end fake-forge test that an item with a row created outside the app
  merges.
- `DESIGN.md` / `PRODUCT.md` — one paragraph on adoption.
