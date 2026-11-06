## ADDED Requirements

### Requirement: A rig with no work is stopped after a quiet window

The app SHALL stop a rig with `rig down <name>`, never `--delete`, once every project bound to it has been quiet
for `rig_lifecycle.idle_after`. A project is quiet when its forge read is fresh and it has no dispatchable item,
no non-terminal queue row and no open change request. The window opens on the first quiet tick; any tick that
finds work closes it.

#### Scenario: The window elapses on an empty project

- **WHEN** every issue on the project is closed, no queue row is live, and the project stays quiet for `idle_after`
- **THEN** one tick runs `rig down <rig>`, appends one `rig.stopped` audit event, and later ticks stop again only
  after the rig is seen running and quiet again

#### Scenario: A stale forge read vetoes the stop

- **WHEN** a project bound to the rig reports a failed or stale forge read, so its item list is empty for that
  reason
- **THEN** no stop is attempted on that tick, because an empty board from a broken read is not an empty board

#### Scenario: Work inside the window cancels it

- **WHEN** an issue appears after three quiet ticks and before `idle_after` elapses
- **THEN** the window resets and no stop happens on that tick or the next

### Requirement: A stopped rig is started before its work is routed

When a rig has no running seat and the plan has a ready item for it, or a claimable row names one of its seats, the
app SHALL launch it with `rig up <spec>` before creating that dispatch's worktree and queue row. A failed launch
SHALL skip the dispatch with one named reason, create no worktree and no row, and be retried on the next tick.

#### Scenario: A new issue wakes a stopped rig

- **WHEN** a ready issue exists, the rig has zero running seats and the tick reaches its dispatch
- **THEN** `rig up <spec>` runs before the worktree and before `rig queue create`, and the row is created for a seat
  the launch started

#### Scenario: The launch fails

- **WHEN** `rig up <spec>` exits non-zero
- **THEN** the tick writes one `act-skip: <key>: rig <rig> up: <error>` line, creates no worktree and no queue row,
  and the still-open issue is dispatched on a later tick once a launch succeeds

#### Scenario: A row resolved while the rig was stopped wakes it

- **WHEN** a row blocked on the operator is answered and becomes claimable while its rig has zero running seats
- **THEN** that row, not a plan intent, triggers the launch, and the row is claimed after the seats are up

### Requirement: The running seats are the only fact about whether a rig is up

Up or down SHALL be read from the seats running on that rig on every tick. A remembered flag, a decision made by
the previous tick, or a `rig.yaml` on disk SHALL NOT be used. A manual `rig up` SHALL never be duplicated, and a
manual `rig down` SHALL stand until work needs the rig.

#### Scenario: The operator starts the rig by hand

- **WHEN** a rig is stopped with a ready issue and the operator runs `rig up` before the next tick
- **THEN** the tick sees the seats, creates the queue row, and runs no second `rig up`

#### Scenario: The operator stops the rig by hand

- **WHEN** the operator runs `rig down` and no work exists
- **THEN** the next tick reads zero seats, finds nothing to start, and records no lifecycle action

#### Scenario: The service restarts

- **WHEN** the app restarts after it had stopped a rig
- **THEN** the first tick re-reads the roster and trusts only what the seats say, never the previous process

### Requirement: A rig is never stopped while a seat may be working

The tick SHALL NOT stop a rig while any non-terminal, unblocked queue row is addressed to a seat on it, or while
any of its seats reports work, whatever the quiet window says.

#### Scenario: A builder is mid-turn

- **WHEN** the project is otherwise quiet but a seat on the rig holds an in-progress row
- **THEN** no stop is attempted, and the window restarts after that row ends

#### Scenario: A row blocked on a person does not hold the rig open

- **WHEN** the only row on the rig is blocked on the operator and the quiet window has elapsed
- **THEN** the rig is stopped, the row stays in the daemon's queue, and answering it later starts the rig again

### Requirement: Lifecycle actions are logged and audited, never announced

Every start and stop SHALL write one log line naming the rig, the action and the reason, plus one audit event
`rig.started` or `rig.stopped`. Neither SHALL send a Telegram message: housekeeping is neither work nor a decision.

#### Scenario: Reading a stop in the log

- **WHEN** a tick stops an idle rig
- **THEN** the log holds one line naming the rig, the action and the quiet duration, the audit log holds one
  `rig.stopped` event, and zero messages are sent

#### Scenario: A failed action is recorded as a failure

- **WHEN** `rig up` or `rig down` fails
- **THEN** one line names the command and its error, and no audit event claims the action succeeded

### Requirement: Lifecycle is opt-in and can be turned off per project

`rig_lifecycle.enabled` SHALL default to false, so an upgraded box keeps today's behaviour. When it is true the
app manages every project's rigs except a project that sets `lifecycle: off`. `--print-config` SHALL show the
block and each project's effective value with its source.

#### Scenario: An existing configuration is unchanged

- **WHEN** the configuration has no `rig_lifecycle` block
- **THEN** no rig is started or stopped by the app, and the poll cadence is the fixed `tick_interval`

#### Scenario: One project opts out

- **WHEN** lifecycle is enabled and a project sets `lifecycle: off`
- **THEN** its rigs are neither stopped when idle nor started when needed

#### Scenario: The operator reads the effective setting

- **WHEN** `--print-config` runs on a box with lifecycle enabled
- **THEN** the output lists `enabled`, `idle_after` and `idle_poll_max` with their sources, and each project's
  effective lifecycle value
