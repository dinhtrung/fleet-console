# Design — what a GC may destroy

## D1. Four keep conditions, and each one is a fact the app holds

A worktree may only be removed when none of these is true:

1. **An open change request's head branch is this worktree's branch.** Asked of the forge, because a squash merge
   leaves the branch's commits unmerged in git's own terms — the operator script learned this the hard way.
2. **A live dispatch row names this item.** The app's own table, in-flight by definition.
3. **A non-terminal queue row names this item.** The rig's table; a builder may be about to claim it.
4. **The working tree is dirty** (`git status --porcelain` non-empty). Uncommitted work cannot be reconstructed
   from the forge, so it is never a GC candidate.

The first three are the "still spoken for" facts; the fourth is the only one that protects the operator. Removal
requires all four to be false, which is why the rule is written as keep-conditions rather than remove-conditions:
each new reason to keep is additive.

## D2. Scope is the project's worktree root, and the main checkout is not a candidate

Only direct children of the project's `paths.worktrees` whose directory name matches `wt-<key>-<n>` are
candidates. The main checkout (`paths.main`) is never touched, and a path that does not resolve under the root is
a refusal, not a revert to `$HOME`. A symlink in the root is logged and skipped, not followed: following it is how a
GC deletes somebody's source tree.

## D3. `git worktree remove`, never `rm -rf`, never `--force`

The command knows about the link, the index and the branch's own bookkeeping; `rm -rf` leaves a broken worktree
record that the next dispatch trips over. `--force` would discard exactly the dirty work D1 protects, so it is
never passed. A refused removal (git's own "contains modified files") is recorded as a keep with git's reason.

## D4. Dry run is the default and the audit is per removal

`--housekeep` prints the plan; `--housekeep --apply` performs it. The tick path is always "apply", because it is
gated by `housekeeping_interval` and by the same dry-run flag being off in config — but its blast radius is the same
four conditions. Each removal writes one `housekeeping.remove` event (worktree, branch, item); one
`housekeeping.summary` event per run carries the counts (removed, kept-open-cr, kept-live, kept-dirty, skipped). A
removal that fails writes the error on that worktree's event, and the summary counts it as failed.

## D5. Never in the same tick as a dispatch for that item

The dispatch phase runs first and the GC runs after it, so an item dispatched on this tick already has a live
dispatch row when the GC looks — D1's second condition keeps its worktree. This is the ordering that makes the tick
safe without a second lock.

## D6. Off by default

`housekeeping_interval: 0` (default) means the tick never runs it. An operator turns it on with a duration
(`6h`); the first run happens one interval after boot, not at boot, so a restart loop cannot become a delete loop.
