## ADDED Requirements

### Requirement: Every write is one event row

Each mutation the app performs SHALL record one event: actor, kind, subject, detail, exit code and a hash of the
command's output. The trail SHALL answer what closed an item or set a label without reading a log.

#### Scenario: Who closed the epic

- **WHEN** an epic is closed by the loop
- **THEN** one event names the actor, the epic and the children listed

### Requirement: Secret material never reaches the audit trail

Tokens, passwords and provider keys SHALL be redacted before an event row is written, and a test SHALL fail the
build when a token-shaped value reaches the store.

#### Scenario: A token in an error message

- **WHEN** a forge call fails with the token in its message
- **THEN** the stored event shows `REDACTED` in that position

### Requirement: An SLA breach escalates once

A decision past its deadline SHALL escalate once, and later ticks SHALL edit or replace that message rather than
send another.

#### Scenario: The same parked task for two hours

- **WHEN** a parked decision stays unanswered for two hours
- **THEN** exactly one escalation names the project, the item, the question and a link to the board

### Requirement: Writes go through the owner, not around it

A mutation of queue or decision state SHALL be a rig CLI call recorded with its argv and exit code. The app SHALL
NOT write that state into its own database.

#### Scenario: A row resolved from the board

- **WHEN** the owner answers a decision on the board
- **THEN** the event records the `rig queue resolve` argv and the row id, and no local state row is written

### Requirement: An audit row is joinable to the rig's transition log

An event that touches a queue row SHALL carry that row's id, so the app's audit and `rig queue transitions` can be
read together for one obligation.

#### Scenario: The chain across both logs

- **WHEN** a row is created, handed off and closed
- **THEN** the app's events and the row's transitions agree on who did what and in which order
