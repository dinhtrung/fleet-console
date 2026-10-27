## ADDED Requirements

### Requirement: Every write is one event row

Each mutation the app performs SHALL record one event: actor, kind, subject, detail, exit code and a hash of the
command's output. The trail SHALL answer what closed a task or a label without reading a log.

#### Scenario: Who closed the epic

- **WHEN** an epic is closed by the loop
- **THEN** one event names the actor, the epic and the children listed

### Requirement: Secrets never reach the audit trail

Tokens and passwords SHALL be redacted before an event row is written, and a redaction test SHALL fail the build if
a token-shaped value reaches the store.

#### Scenario: A token in an error message

- **WHEN** a forge call fails with the token in its message
- **THEN** the stored event shows `REDACTED` in that position

### Requirement: An SLA breach escalates once

A decision past its deadline SHALL escalate once, and later ticks SHALL edit or replace that message rather than
send another.

#### Scenario: The same parked task for two hours

- **WHEN** a parked decision stays unanswered for two hours
- **THEN** exactly one escalation names the project, the task, the question and a link to the board
