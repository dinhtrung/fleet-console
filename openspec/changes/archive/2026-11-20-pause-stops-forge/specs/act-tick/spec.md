## ADDED Requirements

### Requirement: A paused project's tick does nothing

`RunTick` SHALL return an empty applied report, before the gate check, when the project is `onhold` or disabled.
It SHALL NOT read change-request notes, capture verdicts, merge, write notes or create queue rows.

#### Scenario: An accepted change request while paused

- **WHEN** a paused project's open change request carries an ACCEPT and is mergeable
- **THEN** the tick reads no notes, merges nothing and writes nothing

#### Scenario: A dispatch intent while paused

- **WHEN** the plan handed to the tick carries a dispatch intent for a paused project
- **THEN** no queue row, dispatch row or forge marker is written
