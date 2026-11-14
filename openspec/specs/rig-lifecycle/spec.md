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
