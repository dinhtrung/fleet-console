# Proposal — close an epic when its last sub-issue closes

## Why

Tonight the board finished two epics and left both open on GitHub. Epic #68 (progress on the phone) has
three sub-issues and all three are CLOSED; epic #64 (create a project from the board) has two and both are
CLOSED. The service merged the pull requests, closed each sub-issue it merged, and then stopped, because the
only thing it knows how to close is **the item a change request names**: `closes #67` closes #67. An epic is
never dispatched — `internal/decide` skips it with `ReasonEpic` — so no change request ever names it and
nothing ever closes it.

The result is a board and a forge that disagree: the work is done, and the parent that states the goal still
reads open. The operator closes epics by hand, which is exactly the kind of chore this service exists to
remove, and a hand-closed epic is also how an epic gets closed while a sub-issue is still open.

## What changes

- The act loop gains a **rollup** step, run after a merge finalises: if the merged item's parent is an epic
  and every sub-issue of that epic is closed **in the snapshot the tick already read**, the app comments once
  and closes the epic, recording `epic.closed`.
- The rule is conservative: a sub-issue that is missing from the snapshot blocks the close (an incomplete read
  must never close an epic), an epic with one open sub-issue stays open, and an epic already closed is left
  alone.
- Nothing else changes: no new forge call is added to the read path, no new table, and the epic remains
  skipped as a work item — it is only ever *closed*, never dispatched.
