# Design — adoption, and where the guard sits

## D1. The gap is the lookup, not the gate

`Merge` already demands four facts and refuses with a named reason. The hole is one level up: `act.Tick` builds
`latest[item]` from `store.Dispatch` rows and `continue`s when an item is absent (`internal/act/tick.go:126`). The
verdict capture sits below that `continue`, so a hand-dispatched item loses both the merge and the record of the
reviewer's ACCEPT.

Fix in the loop, not in `Merge`: `Merge` stays the function that answers "may this change request merge at this sha",
and a new arm answers "may this item be merged without a dispatch at all".

## D2. Adoption is not a dispatch

The temptation is to write a synthetic `dispatch` row (attempt 0, empty seat) so every downstream path keeps working.
Rejected: `dispatch` carries the seat, the worktree, the branch and the brief — facts the app would be inventing — and
`Generate`/`MergeResult` readers treat a dispatch as "the app put someone on this". A false row would show work that
never happened and would collide with the `UNIQUE(project_id, item_number, attempt)` key the next real dispatch needs.

Instead `Actions.MergeAdopted(ctx, item, request)` takes the request and skips the dispatch-scoped bookkeeping:
`EndDispatch`, `dispatch.merged`. It keeps everything the operator relies on: the merge at the verdict sha, the
`merge` event, `Finalize` (verify closed → close → clear marker → note the next unblocked item), and the returned
`MergeResult` shape so the tick report and the rollup are unchanged.

## D3. Verdict capture moves above the dispatch lookup

`CaptureFromForge(ctx, dispatchID, item, crNumber, headSHA)` needs a dispatch id today. Adoption gives it `0` (or a
nullable key) — the note id and the sha are what make a verdict meaningful, and both come from the change request.
The order change matters more than the signature: capture first, then decide, so "no verdict" in the ledger always
means "the reviewer has not spoken", never "we did not look".

## D4. The three guards, and why the branch prefix is one of them

- `Closes #<item>` in the body — the merge gate's existing contract; a title-only reference is how an item gets
  silently forgotten.
- `ACCEPT` at exactly the head sha — adoption may not invent a verdict; a stale ACCEPT from an earlier head is a
  refusal, not a pass.
- Branch starts with `feature/<item>-` — the app's own convention, and the only cheap way to reject a pull request
  that merely mentions the item. A hand-made branch that follows the convention is indistinguishable from the app's,
  which is intended: the operator's row and the app's row should be treated the same.

A live dispatch on a *different* branch is the dangerous case, so it refuses rather than picking a winner: two
branches closing one item is a human decision about which work survives.

## D5. What stays as it is

- The four facts, the draft→ready step and its ledger event, `Mergeable` and the green check.
- `act-skip` remains the only channel for a refused merge: one line per item per tick, naming the change request.
- No configuration. Adoption is not a mode; it is what the watchdog does when the only thing missing is a row the app
  would have written itself had it dispatched.
