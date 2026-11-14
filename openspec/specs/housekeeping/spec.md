# housekeeping Specification

## Purpose
The reclaim sweep removes a worktree only when nothing can be lost, scoped to the worktree root, dry-run by default and off until an interval is set.

## Requirements

### Requirement: A worktree is removed only when nothing can be lost

The app SHALL keep every worktree for which any of these holds: an open change request's head branch equals its
branch; a live dispatch row names its item; a non-terminal queue row names its item; or its working tree is dirty.
Only when all four are false SHALL it be a removal candidate.

#### Scenario: A shipped item's worktree

- **WHEN** the item is closed, no dispatch or queue row is live, the worktree is clean and no open change request
  matches its branch
- **THEN** the worktree is removed and one `housekeeping.remove` event names it

#### Scenario: An open change request

- **WHEN** an open change request's head branch is the worktree's branch
- **THEN** the worktree is kept, and the summary counts it as kept for an open change request

#### Scenario: Uncommitted work

- **WHEN** `git status --porcelain` in the worktree is non-empty
- **THEN** the worktree is kept whatever its item's state, and the summary counts it as dirty

#### Scenario: The forge read failed

- **WHEN** the change-request read for the project failed this tick
- **THEN** no worktree is removed on that tick

### Requirement: Removal is scoped to the worktree root and never forced

The app SHALL only consider direct children of the project's worktree root whose name matches `wt-<key>-<n>`. It
SHALL remove them with `git worktree remove` without `--force`, SHALL never touch the main checkout, and SHALL skip
a symlink with a log line.

#### Scenario: A path outside the root

- **WHEN** a candidate resolves outside the configured worktree root
- **THEN** it is refused and logged, and nothing is removed

#### Scenario: A symlinked entry

- **WHEN** the root contains a symlink matching the worktree pattern
- **THEN** it is skipped and logged, never followed

### Requirement: Removal is dry-run by default and audited

`--housekeep` SHALL print the plan and change nothing; `--housekeep --apply` SHALL perform it. Every removal and
every run SHALL write an audit event, and a failed removal SHALL be recorded as a failure rather than a success.

#### Scenario: Reading the plan

- **WHEN** the operator runs `--housekeep` on a box with three reclaimable worktrees
- **THEN** three removals are listed and no directory changes

#### Scenario: A refused removal

- **WHEN** git refuses to remove a worktree
- **THEN** its event carries the error and the summary counts it as failed

### Requirement: The tick runs housekeeping on an interval, off by default

The tick SHALL run housekeeping only when `housekeeping_interval` is greater than zero and at least that long has
passed since the previous run, and SHALL run it after the dispatch phase. The default SHALL be off.

#### Scenario: A fresh box

- **WHEN** `housekeeping_interval` is unset
- **THEN** no housekeeping runs and no audit event is written

#### Scenario: The first run after boot

- **WHEN** the interval is `6h` and the service started two minutes ago
- **THEN** nothing runs until six hours have passed

#### Scenario: An item dispatched on this tick

- **WHEN** the tick dispatches an item and its worktree would otherwise be a candidate
- **THEN** the live dispatch row keeps the worktree and the summary counts it as live

### Requirement: The ledger is pruned by a window, never past live work
The housekeeping pass SHALL delete event rows older than `events_keep_days` (default 90) when retention is enabled,
in bounded batches, and SHALL keep every row for an item with a live dispatch or a project with an unresolved queue
row whatever its age.

#### Scenario: Old rows on an idle project
- **WHEN** a project has no live dispatch, no unresolved queue row and events older than the window
- **THEN** those events are deleted in batches and one run event records the counts

#### Scenario: Old rows for work still open
- **WHEN** an item has a live dispatch and its events are older than the window
- **THEN** its events are kept and the run event counts them as kept

#### Scenario: The pass is a dry run
- **WHEN** housekeeping runs without apply
- **THEN** it reports what it would delete and writes nothing

#### Scenario: Retention is disabled
- **WHEN** `events_keep_days` is 0 or unset
- **THEN** no event is deleted and no run event is written
