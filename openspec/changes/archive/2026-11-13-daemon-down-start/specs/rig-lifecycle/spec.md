## ADDED Requirements

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
