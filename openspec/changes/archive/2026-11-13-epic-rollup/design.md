# Design — epic rollup

## Where it runs

In the act loop, immediately after `Finalize` has closed the merged item — `internal/act/tick.go`, the same
place the merge result is turned into events. Not in `internal/decide` (that package is pure and has no
writes) and not in the mirror (the mirror records, it does not act).

## The inputs it already has

A tick holds `in.Items`: every open and closed item of the project with `Number`, `Kind`, `Parent` and
`State`, straight from the forge in the same read the plan was computed from. The epic and its children are
both in there, so the rollup costs **no forge call** and cannot disagree with the plan it just acted on.

## The rule, in order

Given the merged item `M` and the snapshot `S`:

1. `M.Parent` is nil → nothing to roll up. (Most items.)
2. The parent is not `KindEpic` → nothing to roll up. A plain issue that happens to be a parent is not a goal.
3. Every item in `S` whose `Parent == M.Parent` is closed, and at least one child was seen → else stop: a
   sub-issue is open, or the snapshot lost the children and a partial read must never close an epic.
4. The parent itself is still open → else stop (idempotent, and an epic closed by hand is respected).

Then, once: `Comment(parent, <what landed and that this closes the epic>)`, `CloseItem(parent)`, and the event
`epic.closed` with the child count.

## Failure handling

- A failed comment or close must **not** fail the tick that already merged and closed the child: the merge is
  the fact, the rollup is a courtesy. The failure goes into the tick's `Skipped` list as
  `#<epic> rollup: <err>` and is retried on the next tick, when the same three conditions still hold.
- No dedupe table: condition 4 (parent still open) is the dedupe. A second attempt sees a closed epic and does
  nothing, so the worst case of a crash between close and event is a missing audit line, never a double close.
- No reopen: an epic closed by a person stays closed even if a new sub-issue is later added to it, because the
  rollup only ever closes, never opens.

## Why not the forge

GitHub can close the parent automatically (project workflows), GitLab cannot, and the app is the single writer
for this repo's state — an operator would then have two sources of truth for "is this epic closed". The app
decides, the forge records.
