# rig-lifecycle Specification

## Purpose
Starting and stopping a project's rig: demand is the only reason to start, a down daemon is discovered rather than assumed, and every start carries its reason.

## Requirements

### Requirement: A daemon that is not running makes every rig down
A rig read that fails because no daemon answers SHALL be reported as `rig.ErrDaemonDown`, and the tick SHALL carry
that fact as `RigDown` alongside the existing stale flag.

#### Scenario: The daemon's own sentence
- **WHEN** `rig ps` exits with `Daemon not running. This command needs a running daemon.`
- **THEN** the adapter returns an error satisfying `errors.Is(err, rig.ErrDaemonDown)`

#### Scenario: A refused local connection
- **WHEN** the rig binary cannot reach the daemon socket and reports a refused connection
- **THEN** the error is also `rig.ErrDaemonDown`

### Requirement: Only a missing daemon counts as down
Any other rig-read failure SHALL NOT be classified as down, and the tick SHALL keep its current behaviour of
deciding nothing.

#### Scenario: A timeout on a busy box
- **WHEN** the rig read times out while the daemon is alive
- **THEN** the error is not `rig.ErrDaemonDown` and no start is planned

### Requirement: A down rig is started when a managed project has demand
With `RigDown` set, the lifecycle rule SHALL treat the rig as stopped with a known roster, and SHALL start it when a
managed project has a fresh read and at least one ready item.

#### Scenario: A reboot with ready work
- **WHEN** the daemon is down and a managed project's fresh read reports three ready items
- **THEN** one start is planned for that rig, its reason naming the three items

#### Scenario: Nothing to do
- **WHEN** the daemon is down and every managed project reports zero ready items
- **THEN** no start is planned

### Requirement: A stale read is never demand
The start SHALL require a fresh forge read, so a failed or stale read cannot launch a fleet.

#### Scenario: Forge read failed while the daemon is down
- **WHEN** the project's forge read is stale and the daemon is down
- **THEN** no start is planned for its rig

### Requirement: The stop half stays dark while the daemon is down
A down daemon SHALL NOT produce a stop action, because there is no running fleet to stop.

#### Scenario: Quiet projects and a dead daemon
- **WHEN** the daemon is down and every project has been quiet longer than `idle_after`
- **THEN** no stop is planned for any rig

### Requirement: A start attempt is not repeated inside the backoff
The executor SHALL attempt at most one start per rig per `StartBackoff` (5 minutes), keeping that state in memory,
and a refused attempt SHALL be a deduplicated log line rather than an event or a notification.

#### Scenario: A start that keeps failing
- **WHEN** a start fails and the next ticks still see demand and a dead daemon
- **THEN** no second `rig up` runs until the window has passed, and one log line says so

### Requirement: A cold launch gets its own timeout
`rig up` SHALL run with a start timeout of five minutes, independent of the twenty-second default that bounds a
read.

#### Scenario: A cold boot exceeds the read budget
- **WHEN** a start launches a daemon and its seats and takes longer than twenty seconds
- **THEN** the call is still running, and a slow success is not recorded as a failure

### Requirement: The start reason reaches the log and the audit
A successful start SHALL log `rig-lifecycle: <rig> started — <reason>` and append the existing `rig.started` event
whose detail names the daemon's state and the demand that triggered it.

#### Scenario: Reading why the fleet came up
- **WHEN** a down daemon is started because three items were ready
- **THEN** the event detail reads `daemon down; 3 ready item(s)`

### Requirement: A rig with demand is started even when its roster cannot be read
When the rig read fails, a rig whose last known state had no running seat SHALL be treated as stopped and started once if a fresh managed project on it has ready items and the app's own newest lifecycle event for that rig is a stop (the same provenance the known-stopped arm requires, design D6); a rig whose last known state had running seats, or that this process has never read, SHALL NOT be started on an unread roster.

#### Scenario: Stopped rig, stale read, ready work
- **WHEN** the rig read is stale, the last known seat count was 0, the app's newest lifecycle event for it is `rig.stopped`, and a ready item exists on a bound project
- **THEN** one start is planned with a reason naming the unknown roster and the item count

#### Scenario: A rig stopped by hand, stale read
- **WHEN** the rig read is stale, the last known seat count was 0, and the app has written no `rig.stopped` event for it
- **THEN** no start is planned, because the operator's stop stands even while the read is failing

#### Scenario: Live rig, stale read
- **WHEN** the rig read is stale and the last known seat count was above 0
- **THEN** no start is planned, because a start on unknown data could duplicate a live fleet

#### Scenario: Stale read, no demand
- **WHEN** the rig read is stale and no project has ready items
- **THEN** no start is planned

### Requirement: Every rig gets one lifecycle line per tick
Each tick SHALL log one line per rig it considered, naming the decision (start, stop or none) and the facts that produced it: running seats, ready items, whether the roster was known, and the reason.

#### Scenario: A rig that needs nothing
- **WHEN** a rig is running and quiet inside its window
- **THEN** the line says `none` and carries running and ready counts

#### Scenario: A rig started on an unknown roster
- **WHEN** a start is planned because the roster could not be read
- **THEN** the line names `roster unknown` and the item count, not a bare `running=0`

### Requirement: A ready item that is not dispatched says why
When the plan leaves a ready item undispatched for a reason the operator can act on, the tick SHALL write one line naming the item and the plan's reason, once per (item, reason, head) — where the head is the item's open change request — and SHALL NOT repeat it on a later tick while those three are unchanged.

#### Scenario: No free builder
- **WHEN** every admitted seat is taken and an item is ready
- **THEN** one line names the item and `no_free_builder`, and the next tick with the same item, reason and head writes nothing

#### Scenario: The head moves
- **WHEN** the same item is skipped for the same reason but its change request now has a different head
- **THEN** the line is written again, because the old head's refusal says nothing about the new one

#### Scenario: In-flight state stays quiet
- **WHEN** the item is already dispatched or a live row holds it
- **THEN** no line is written: that is ordinary in-flight state, not a refusal

### Requirement: A stopped rig with demand is started, but only if this app stopped it
With a healthy daemon and no running seat, the lifecycle rule SHALL start a rig at most once per `StartBackoff` when a
managed project has a fresh read and at least one ready item, carrying `stopped_with_demand: <n> ready item(s), no
running seat` as the reason. It SHALL start that rig only when the app's own newest lifecycle event for it is a stop:
a rig stopped by hand (`rig down`, `rig seat stop`) SHALL NOT be started by this path.

#### Scenario: Our own quiet stop and new work
- **WHEN** the app stopped a rig for being quiet and a managed project's fresh read reports ready items
- **THEN** one start is planned with the `stopped_with_demand` reason, and a second tick inside `StartBackoff` launches nothing

#### Scenario: Stopped by hand
- **WHEN** a rig is stopped, the app has written no `rig.stopped` event for it, and a project has ready items
- **THEN** no start is planned, because the operator's `rig down` stands

#### Scenario: A stale read with a stopped rig
- **WHEN** the rig is stopped and the project's forge read is stale
- **THEN** no start is planned

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

When a rig has no running seat, the app's own newest lifecycle event for it is a stop, and the plan has a ready item
for it — or a claimable row names one of its seats — the app SHALL launch it with `rig up <spec>` before creating that
dispatch's worktree and queue row. A rig stopped by hand (`rig down`, `rig seat stop`) SHALL NOT be started by this
path. A failed launch SHALL skip the dispatch with one named reason, create no worktree and no row, and be retried on
the next tick.

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
