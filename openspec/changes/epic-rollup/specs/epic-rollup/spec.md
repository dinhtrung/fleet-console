## ADDED Requirements
### Requirement: An epic closes when its last sub-issue closes
When an applied tick merges a change request and closes its item, the app SHALL close that item's parent epic
if the parent is an epic and every sub-issue of that epic is closed in the snapshot the tick read.
#### Scenario: The last child of a three-child epic merges
- **WHEN** the merge closes #67 and #65 and #66 are already closed under epic #68
- **THEN** the app comments once on #68 saying what landed and that the epic is complete
- **AND** it closes #68
- **AND** the event `epic.closed` records the epic number and the number of children
### Requirement: One open sub-issue keeps the epic open
An epic with at least one open sub-issue SHALL stay open, and the tick SHALL say so in its skip list.
#### Scenario: A two-of-three epic
- **WHEN** #83 is still open under epic #84 and a sibling merges
- **THEN** #84 stays open
- **AND** the tick records `#84 rollup: 1 sub-issue still open (#83)`
### Requirement: An incomplete snapshot never closes an epic
If the snapshot the tick read contains no sub-issues for the parent, the app SHALL NOT close the parent, because
an empty read is indistinguishable from "all children closed".
#### Scenario: The forge read returned the epic but not its children
- **WHEN** the merged item's parent is an epic and the snapshot has zero items whose parent is that epic
- **THEN** no close happens and the tick records `#<epic> rollup: no sub-issues in the snapshot`
### Requirement: Only an epic parent is rolled up
The rollup SHALL apply only when the parent's kind is `epic`; a non-epic parent SHALL be left alone.
#### Scenario: A plain issue used as a parent
- **WHEN** the merged item's parent is a task, not an epic
- **THEN** the parent is not closed and nothing is recorded as a rollup
### Requirement: An already closed epic is left alone
The rollup SHALL skip a parent that is already closed, which is what makes the step idempotent.
#### Scenario: Two ticks after the close
- **WHEN** the next tick processes the same project with epic #68 already closed
- **THEN** no comment, no close and no event are produced
### Requirement: A failed rollup does not fail the tick
A failed comment or close after a merge SHALL be reported in the tick's skip list and retried on the next tick;
the merge that already landed SHALL NOT be reported as failed.
#### Scenario: The close call errors
- **WHEN** `CloseItem` for the epic answers an error after the merge landed
- **THEN** the tick's skip list contains `#<epic> rollup: <error>` and the merge result still reads merged
- **AND** the next tick retries, because the epic is still open
### Requirement: The close is attributed and readable
The comment on the epic SHALL name the item that finished it and the pull request that carried it, so an operator
reading the epic later can tell why it closed without opening the audit log.
#### Scenario: Reading the closed epic
- **WHEN** the operator opens the closed epic
- **THEN** the last comment names #<item>, the pull request number and that every sub-issue is closed
### Requirement: The board and the forge agree after a rollup
The epic SHALL leave the board's open set on the tick after the close, with the closed state the forge reports.
#### Scenario: The tick after the rollup
- **WHEN** the next mirror tick runs after the epic was closed
- **THEN** the epic reads closed on the board and disappears from the waiting list
