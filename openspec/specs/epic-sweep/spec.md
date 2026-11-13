# epic-sweep Specification

## Purpose
The per-tick sweep closes an epic whose sub-issues are all closed, bounded to one sub-issue read per open epic and once per epic.

## Requirements

### Requirement: An epic whose sub-issues are all closed is closed by the sweep
The app SHALL decide, for an open epic it observes and a read of that epic's sub-issues covering open and closed
children, that the epic is closed when at least one child exists and no child is open.
#### Scenario: The last child closed outside a merge
- **WHEN** epic #84 is open and its read returns #81, #82 and #83, all closed
- **THEN** the decision is a close carrying the epic number and three children
- **AND** the decision reports that the epic must be closed

#### Scenario: A sweep close carries its child count
- **WHEN** the decision closes an epic over a read of five closed children
- **THEN** the decision's child count is five and its open list is empty
- **AND** the child count is the one the close records and comments, not a number recomputed later

### Requirement: The zero-children guard holds against the sweep's own read
The app SHALL NOT close an epic whose read returned no child whose parent is that epic, because an empty read is
indistinguishable from "all children closed" and closing over it would close an epic over work nobody looked at.
#### Scenario: The read comes back empty
- **WHEN** epic #84 is open and the read returns no item whose parent is #84
- **THEN** the decision is a skip whose reason is `no sub-issues in the read`
- **AND** no close, comment or event happens for #84

#### Scenario: A read that returned only another epic's children
- **WHEN** the read for #84 returns only #90, whose parent is #91
- **THEN** the decision is the zero-children skip, because #90 is not a child of #84

### Requirement: An epic with an open child stays open without per-tick noise
An epic with at least one open child SHALL stay open, and its skip SHALL be marked quiet so a sweep that meets the
same unfinished epic on every tick does not print the same line in the tick's journal each time.
#### Scenario: A two-of-three epic met on consecutive ticks
- **WHEN** #83 is open under epic #84 and the read returns #81, #82 closed and #83 open
- **THEN** the decision is a skip whose reason is `1 sub-issue still open (#83)`
- **AND** the decision is marked quiet, so the tick's skip list stays empty for #84
- **AND** the next tick's sweep gives the same verdict and again writes no line

#### Scenario: Two open children named in number order
- **WHEN** the read returns #95 and #83 open under epic #84
- **THEN** the reason is `2 sub-issues still open (#83, #95)`, with the numbers sorted

### Requirement: The sweep leaves an already-closed epic and a non-epic row alone
The sweep SHALL skip an epic whose own state is not open, quietly and without a write, and SHALL skip a row whose
kind is not `epic`; both are the ordinary repeat sighting rather than a considered decline.
#### Scenario: The tick after the close
- **WHEN** the next tick's sweep meets epic #84, whose state now reads closed
- **THEN** the decision is a quiet skip whose reason is `epic already closed`
- **AND** no comment, no close and no event are produced for #84

#### Scenario: A plain issue offered as the candidate
- **WHEN** the candidate row for #84 is a task, not an epic
- **THEN** the decision is a quiet skip whose reason is `not an epic`, reached without consulting the read
- **AND** nothing is written for #84

### Requirement: The decision is pure
The sweep decision SHALL be a pure function of the candidate row and the read — no clock, no forge, no store — so the
same inputs always give the same verdict, the caller may act on it and retry safely, and the decision can be tested
without a forge standing in for its argument.
#### Scenario: The same read twice
- **WHEN** the sweep is called twice with the same epic row and the same read
- **THEN** both calls return the same kind, reason, child count, open list and quiet flag
- **AND** neither call modifies the read it was given

### Requirement: A close names the sweep as its source
A close the sweep decides SHALL name the sweep as its source, in the comment on the epic and in the `epic.closed`
event detail, so an operator reading the epic or the audit log tells a sweep close from a merge close.
#### Scenario: An operator reads the audit log after a sweep close
- **WHEN** the sweep closes epic #84 over three closed children
- **THEN** the event kind is `epic.closed` with detail `sweep: 3 sub-issues`
- **AND** the comment on #84 opens by naming the sweep as the cause

### Requirement: A retried sweep close posts one comment, not one per tick
The sweep SHALL post at most one comment on an epic it is closing, however many ticks the close is retried across. The
comment precedes the close because a failed comment must leave the epic open for a retry; the retry SHALL therefore
recognise the comment already on the epic rather than posting another, so a close that stays broken does not grow the
comment count without bound.
#### Scenario: The close fails for several ticks
- **WHEN** the sweep decides to close #84 and the close answers an error on each of three consecutive ticks
- **THEN** #84 carries exactly one sweep comment after the third tick
- **AND** no `epic.closed` event has been recorded, and #84 is still open
#### Scenario: The close recovers after a failure
- **WHEN** the close that had been failing succeeds on a later tick
- **THEN** #84 is closed and carries exactly one sweep comment
- **AND** exactly one `epic.closed` event with detail `sweep: <N> sub-issues` is recorded

### Requirement: A failed read of the epic's notes leaves the epic open
The dedupe read that decides whether an epic already carries a sweep comment SHALL treat a failed read as a failure,
never as "no comment yet". A failed notes read SHALL become a skip line naming the error and the tick SHALL still
succeed; the epic stays open and no comment or close is written, so the next tick reads the notes afresh and the
comment count does not grow while the failure lasts.
#### Scenario: The notes read errors while the close is also failing
- **WHEN** the sweep decides to close #84 and the notes read answers an error on three consecutive ticks
- **THEN** each tick's skip list contains `#84 sweep: read notes: <error>` and the tick reports success
- **AND** #84 carries no sweep comment and no `epic.closed` event has been recorded, because the read never returned
#### Scenario: The notes read recovers after failing
- **WHEN** the notes read succeeds on a later tick with the same closed children
- **THEN** #84 is closed carrying exactly one sweep comment and exactly one `epic.closed` event

### Requirement: A failing sweep write does not fail the tick and is retried
A failed comment or close SHALL become a skip line naming the error and the tick SHALL still succeed; the epic stays
open, so the next tick's sweep sees the same conditions and tries again without a dedupe record.
#### Scenario: The close call errors
- **WHEN** the sweep decides to close #84 and the close answers an error
- **THEN** the tick's skip list contains `#84 sweep: <error>` and the tick reports success
- **AND** #84 is still open for the next tick's sweep
