## ADDED Requirements

### Requirement: A quiet snapshot sends no progress notice

The app SHALL NOT send a progress notice when the snapshot is quiet. A snapshot is quiet when **all** of these hold:
the count of open epics is zero, the in-flight bucket is empty, the pending bucket is empty, the on-hold bucket is
empty, and there is no open decision. The rule SHALL apply to every trigger: the digest interval and a merge inside
the applied tick SHALL both be suppressed while the snapshot is quiet.

#### Scenario: The digest fires on an empty board

- **WHEN** every issue is closed, no epic is open, nothing is in flight and no decision waits, and the digest
  interval opens
- **THEN** no Telegram message is sent, and the board still renders `idle — no open work: every issue is closed`

#### Scenario: The last merge empties the board

- **WHEN** the merge watchdog merges the pull request that closes the last open issue and the snapshot taken after
  the merge is quiet
- **THEN** no progress notice is sent for that tick

#### Scenario: Open work keeps the notice

- **WHEN** one issue is pending and every seat is idle waiting for a dispatch
- **THEN** the progress notice is still sent, because the board holds work the operator has to see

#### Scenario: A waiting decision is never quiet

- **WHEN** an open decision is waiting on the operator and nothing else is open
- **THEN** the snapshot is not quiet, so the progress notice and the decision message both behave exactly as before

### Requirement: The quiet skip is visible in the log

When a trigger fires and the notice is skipped because the snapshot is quiet, the app SHALL write one line naming
that reason, so an operator reading the log can tell a deliberate skip from a failed send.

#### Scenario: Reading a quiet tick in the log

- **WHEN** an applied tick opens the digest gate on a quiet board
- **THEN** the log contains exactly one line stating that nothing was sent because there is no open work, and no
  transport error is logged

### Requirement: The on-demand report is exempt from the quiet rule

`fleet-console --notify-report` SHALL send on demand even when the snapshot is quiet. It is an explicit request from
the operator, not an unsolicited trigger.

#### Scenario: Asking for a report on an empty board

- **WHEN** the operator runs `--notify-report` while every issue is closed
- **THEN** the message is sent and its exit code is 0

### Requirement: Dedupe state survives a quiet skip

A skipped quiet notice SHALL NOT be recorded as the last reported state. The next non-quiet snapshot SHALL be sent
even when its rendered body equals a body sent before the quiet period.

#### Scenario: Work resumes after a silent stretch

- **WHEN** the board was quiet for a day and a new issue is then opened and dispatched
- **THEN** the next notice is sent, not suppressed as a duplicate of the pre-quiet message
