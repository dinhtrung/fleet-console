## ADDED Requirements

### Requirement: A completion notice is sent when code lands
When an applied tick merges a change and closes its issue, the app SHALL send exactly one Telegram message naming
what landed and reporting the current state of the project.

#### Scenario: A merge in an applied tick
- **WHEN** the merge watchdog merges the pull request that closes #62
- **THEN** one message is sent, its first line names the project, the issue number and the title
- **AND** it names the pull request, the branch and the seat that built it

### Requirement: The message carries the whole project record
The message SHALL report every field of the project the service reads: key, name, forge, repository slug,
repository URL, default branch, concurrency cap, on-hold, act on/off, and both configured paths.

#### Scenario: Reading the message without the board
- **WHEN** the operator reads a completion notice for a project with `act: true` and `onhold: no`
- **THEN** both facts appear in the message, with the checkout and worktree paths it is running
- **AND** a project on hold says `onhold: yes`, which explains why nothing else moved

### Requirement: The message names the rig and its roster
The message SHALL name every rig the project is assigned to, and SHALL report the seats and the busy count when
the rig answered.

#### Scenario: The rig answered
- **WHEN** the tick read a roster of three seats with two dispatched items
- **THEN** the rig block names all three seats and states `busy 2/3`

#### Scenario: The rig did not answer
- **WHEN** the rig read fails during the tick
- **THEN** the rig block says `roster unavailable` with the reason, and the message is still sent

### Requirement: The message reports the four work buckets
The message SHALL report in progress (issue, seat, branch, age), pending, on hold, and the epic the items belong
to, each with its true count, and SHALL print a line for an empty bucket.

#### Scenario: One item in flight and nothing else
- **WHEN** the project has one dispatched item and no other open item
- **THEN** `in progress (1)` lists it, `pending (0)` and `on hold (0)` still appear, and the epic line names the
  parent epic with its done/total count

### Requirement: One state, one message
The app SHALL NOT send a second message for a state it has already reported, and SHALL send a new message when
the state changes.

#### Scenario: A quiet tick
- **WHEN** two ticks produce the same report
- **THEN** only the first one is sent

### Requirement: A failed send is never a failed tick
When Telegram answers an error or times out, the app SHALL record the failure, print it, and continue: the tick,
the merge and the board SHALL be unaffected.

#### Scenario: Telegram is down
- **WHEN** the Bot API returns an error after the retry
- **THEN** the merge is still recorded, the tick returns without an error, and `progress.failed` is in the audit log

### Requirement: Without credentials the feature is off, not broken
When `TELEGRAM_BOT_TOKEN` or `TELEGRAM_CHAT_ID` is unset, the app SHALL log one line at boot and SHALL send
nothing.

#### Scenario: A box that has no bot token
- **WHEN** the service starts without the two variables
- **THEN** it logs that completion notices are off, serves the board normally, and every trigger is a no-op

### Requirement: The report can be asked for
`fleet-console --notify-report` SHALL send the current report without waiting for a merge, and SHALL exit 0 when
the message was delivered, 1 when the transport failed, and 3 when notifications are not configured.

#### Scenario: Asking where things stand
- **WHEN** the operator runs `fleet-console --notify-report` on a configured box
- **THEN** one message with the same shape is delivered and the command exits 0

### Requirement: A long report stays inside one message
The rendered report SHALL stay under 3900 characters: at most five entries per bucket plus a `+N more` line, and
the project and rig blocks SHALL survive any truncation.

#### Scenario: Forty items in one project
- **WHEN** the project has more open items than the caps allow
- **THEN** each bucket lists five and states the real total, and the whole body is shorter than 3900 characters

### Requirement: The repository URL has one rule
The message's repository URL SHALL come from `repo_url` when the config sets it, otherwise be derived from the
forge and the repository slug, and a GitLab project without `repo_url` SHALL raise a config warning.

#### Scenario: A self-hosted GitLab
- **WHEN** a GitLab project has no `repo_url`
- **THEN** the URL is derived as `https://gitlab.com/<slug>` and the config prints a warning telling the operator
  to set `repo_url`

### Requirement: Alerts reach the phone
Every escalation SHALL be delivered through the same Telegram client the progress notices use, and the two kinds
of message SHALL never be confused: a decision message is edited as its state changes, a progress message is
never edited.

#### Scenario: A decision and a merge in the same tick
- **WHEN** a decision is escalated and a different issue merges
- **THEN** the decision has its own editable message and the merge has its own, and neither is edited by the other
