## ADDED Requirements

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
