# Design — the epic sweep

## Why a second function instead of a second caller of `Rollup`

`Rollup(parent *int, items []forge.Item)` takes the **merged item's parent pointer** as its way in. A tick that
merged nothing has no merged item and therefore no parent pointer, so there is no argument to hand it. Reaching it
by fabricating one would mean the sweep pretending an epic was "the parent of something that just merged", which is
the one fact the sweep does not have. `Sweep(epic forge.Item, items []forge.Item)` starts where the sweep actually
is — an epic row the tick observed — and keeps the rest of the rule identical. Both functions coexist; neither
calls the other, and the merge path's behaviour is untouched.

`Rollup` returns `(Decision, error)` because its shipped callers code against that signature. `Sweep` has no shipped
caller, so it returns a bare `Decision`: no input can fail, and a nil error nobody can cause is a promise the
function does not keep. The decision type is shared, so a caller handling a `Rollup` verdict handles a `Sweep`
verdict with the same code.

## The inputs it needs, and the read that supplies them

**This is the trap #122 names, stated here so it is not rediscovered.** `openIssues` lists `state=open` only —
`internal/forge/github.go:163` (`issues?state=open`) and `internal/forge/gitlab.go:147` (`issues?state=opened`).
Everything downstream is built from that list: `byNumber` at `github.go:56` and its fill at `github.go:72`, the
parent-marking loop at `github.go:118-122`, and the item slice returned at `github.go:152-157`. So a tick's
`in.Items` holds the open epics and their open sub-issues and **no closed child at all**. Against that slice every
open epic reads either "zero sub-issues" or "all sub-issues closed", depending on how the count is taken, and
neither reading is the truth.

The states themselves are already being fetched and thrown away. `Items` calls `g.subIssues` once per open item
(`github.go:88-98`), and that response carries every child's state — the fixture
`internal/forge/testdata/github/issue-1-sub-issues.json` returns ten children of which three are `closed`, and
`ghChild` declares both fields (`github.go:180-183`). The parent-marking loop keeps a child only when the child is
itself in `byNumber`, which is the open list, so the three closed children are dropped at `github.go:120`. The cost
of knowing "which children are closed" was paid in the read that already ran; what is missing is the hand-off, not
the request.

So the sweep's evidence cannot be the tick's snapshot, and the write half (#124) has two ways to supply it. This
change takes that read as an argument and says nothing about which way it is obtained — the decision is testable
without a forge, which is the point of keeping it pure. What the write half inherits:

- **Surfacing the read `Items` already made** needs no new request and no new budget line, but it means changing
  `Items`'s output — either closed children as `Item` rows or a parallel map — which touches both adapters and every
  consumer of the slice. That is a wider change than the sweep, and it is not this change's to make.
- **Re-reading per open epic** is what #124's scope describes ("ask the forge for each open epic's sub-issues").
  The second `sub_issues` call per open epic is counted by the budget in `internal/forge/cli.go:82`, so the sweep
  spends one request per open epic per tick on top of the list read and the per-item reads `Items` already spends
  (`github.go:93-98`). Bounded and visible, but not free — and it duplicates data the tick already held.
- A read that fails leaves the epic **open**, not closed. `ErrStale` and `ErrBudget` are the ordinary way a read
  fails (`internal/forge/forge.go:48-50`); a sweep that treated an error as "no open children" would close epics
  over work nobody looked at. `ErrStale`'s doc comment says the same thing in the opposite direction: a stale read
  "must not read as 'no tasks'" (`forge.go:46-47`).

## The rule, in order

Given an epic row `E` and a read `R` of its sub-issues:

1. `E.Kind != KindEpic` → skip, `not an epic`. A plain issue is not this step's subject. **Quiet.**
2. `E.State != open` → skip, `epic already closed`. This is the idempotence: the sweep meets the same epic on every
   tick, and the second sighting is the ordinary case. **Quiet.**
3. Count the items in `R` whose `Parent == E.Number`; an item with no parent, or another parent, is not a child. The
   epic's own row is not its own child.
4. `children == 0` → skip, `no sub-issues in the read`. An empty read is indistinguishable from "all closed", and a
   short read must never close an epic over work nobody looked at. **Journaled.**
5. Any child still open → skip, `N sub-issues still open (#a, #b)`, numbers sorted. **Quiet.**
6. Otherwise → close, carrying the child count.

Then, in the write half: one comment, one `CloseItem`, one `epic.closed` event.

### Why steps 1, 2 and 5 are quiet and step 4 is not

The tick's `Skipped` list is read by an operator looking for what did not happen. The rollup can afford a line per
skip because it runs once per merge — news by construction. The sweep runs **once per open epic per tick**, so a
line for every unfinished epic would print the same sentence every tick until its last child closes, and the
journal would bury the one skip that means something. That is #122's requirement in the operator's words: "must not
put a skip line in the journal on every tick".

Step 4 stays journaled because it is not the ordinary state of an epic; it means the read came back short. It is the
one case where silence would hide a defect. Step 1 and step 2 are quiet for the same reason as 5 — they repeat
forever — and step 2 additionally carries no new fact after the first sighting.

`Quiet` is a field on `Decision`, set only by `Sweep`; `Rollup` leaves it false, so the merge path's journaling is
unchanged by this change. The field rather than a reason-string comparison: `journalsSkip` in `rollup_tick.go`
switches on the exact reason text, which is brittle in the one place a typo silently changes behaviour. A decision
carries its own answer. The write half (#124) reads `Quiet` when it decides whether to append the skip line, and
`journalsSkip` stays as it is for the merge path.

## Attribution: telling a sweep close from a merge close

The merge path records `epic.closed` with subject `#<epic>` and detail `<N> sub-issues`
(`internal/act/rollup_tick.go:62-63`). The sweep records the same kind — operators already query `epic.closed`, and a
second kind would split the query — with a detail that names the source, `sweep: <N> sub-issues`. The comment opens
by naming the sweep for the same reason: the operator who opens the epic later sees why it closed without reading
the audit table.

Not chosen: a distinct kind `epic.swept`. It reads better in isolation and worse in use — "which epics closed this
week" becomes a union of two queries, and every existing consumer of `epic.closed` would silently miss sweep
closes. The source belongs in the detail, where the readers already look.

## Failure handling

The write half's failures follow the rollup's courtesy rule: a failed comment or close becomes a skip line and the
tick still succeeds. It matters more here than in the merge path, because a sweep close has no merge behind it to
carry the fact — but the retry is stronger, not weaker: the epic is still open, so the next tick's sweep sees the
same conditions and tries again. No dedupe table is needed; "the epic is still open" is the dedupe.

## What this change does not do

No tick wiring, no forge call, no store row. `Sweep` is unreferenced by production code until #124 calls it. That is
deliberate — #122 splits spec-and-decision from the write so the rule can be reviewed and tested without a forge
fake standing in for the argument being reviewed — and it is why the acceptance for this change is a green table
test rather than a tick-level assertion.
