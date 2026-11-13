## ADDED Requirements

### Requirement: A rig with demand is started even when its roster cannot be read
When the rig read fails, a rig whose last known state had no running seat SHALL be treated as stopped and started once if a fresh managed project on it has ready items; a rig whose last known state had running seats SHALL NOT be started on an unread roster.

#### Scenario: Stopped rig, stale read, ready work
- **WHEN** the rig read is stale, the last known seat count was 0 and a ready item exists on a bound project
- **THEN** one start is planned with a reason naming the unknown roster and the item count

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
When the plan leaves a ready item undispatched, the tick SHALL write one line naming the item and the plan's reason, and SHALL NOT repeat it on every tick while the reason is unchanged.

#### Scenario: No free builder
- **WHEN** every admitted seat is taken and an item is ready
- **THEN** one line names the item and `no_free_builder`, and the next tick with the same state prints nothing
