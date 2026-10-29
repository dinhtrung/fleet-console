## ADDED Requirements

### Requirement: One instance owns the database

The act loop SHALL take an exclusive lock on `<database>.lock` before it reads or plans. A second process SHALL exit
with `another instance owns <db>` and SHALL NOT read, plan, dispatch or merge. The lock SHALL be released on exit and by
the kernel when the process dies.

#### Scenario: Two instances, one database

- **WHEN** a second `fleet-console --apply` starts against a database whose lock is held
- **THEN** it exits before planning, and the first instance is the only writer

#### Scenario: A killed holder

- **WHEN** the holding process is killed with `SIGKILL`
- **THEN** the next start takes the lock without a timeout and ticks normally

### Requirement: A verdict on a moved head is stale, and it asks again

A verdict SHALL apply only to the sha it names. When the head differs, the item SHALL show `verdict stale` with both
shas, one decision SHALL be raised to the reviewer naming the new head, and the merge gate SHALL refuse. A stale verdict
SHALL never be read as an accept, and the decision SHALL be raised once per head, not once per tick.

#### Scenario: A builder merges main after the verdict

- **WHEN** a reviewer posts `ACCEPT a1b2c3` and the builder then merges `main`, moving the head to `d4e5f6`
- **THEN** the item shows `verdict stale a1b2c3 → d4e5f6`, the reviewer is asked once, and nothing merges

#### Scenario: The reviewer accepts the new head

- **WHEN** the reviewer posts `ACCEPT d4e5f6` and every other gate holds
- **THEN** the merge proceeds, and the stale state clears

### Requirement: Dispatch is two-phase and adopted at boot

A dispatch SHALL be recorded `pending` before its queue row is created, then stamped with the row id. On boot, a
`pending` dispatch SHALL be adopted when exactly one row matches its branch or brief; with no match, or more than one,
it SHALL be marked `abandoned` and a decision SHALL name the item.

#### Scenario: A crash between the row and the record

- **WHEN** the process dies after the queue row exists and before the record is stamped
- **THEN** the next start adopts that row, and the item stays `building` with one row

#### Scenario: Two matching rows

- **WHEN** a `pending` dispatch matches two rows
- **THEN** it is abandoned, no row is closed, and one decision asks which row is real

### Requirement: The rig wins every conflict

Each tick SHALL reconcile dispatch records against rig rows before planning. A terminal row whose change request is not
merged SHALL end the dispatch `superseded`. A merged change request SHALL end it `merged`. A record with no matching row
for one full tick SHALL end it `abandoned`. Reconciliation SHALL write one event per transition and SHALL NOT dispatch
in the same tick.

#### Scenario: A row closed by hand

- **WHEN** an operator closes the queue row of a building item
- **THEN** the next tick ends the dispatch `superseded`, the item leaves `building`, and one event records it

### Requirement: Tiers are filled in order and crossed only by their trigger

Within a tier, rigs SHALL be filled in listed order. A later tier SHALL receive work only when its boundary trigger
holds: `on-full` when the earlier tier has no free builder, `on-unavailable` when no rig in it is healthy. A tier that is
full under `on-unavailable` SHALL wait rather than spill.

#### Scenario: Fill-first inside a tier

- **WHEN** tier 1 lists two rigs with one free builder each and two items are ready
- **THEN** the first item goes to the first rig and the second to the second, in that order

#### Scenario: A full primary with a standby behind it

- **WHEN** tier 1 is full but healthy and tier 2 is `on-unavailable`
- **THEN** no dispatch crosses the boundary, and the board names the wait as capacity, not failure

### Requirement: Failover follows health, not load

A rig SHALL be unhealthy when a seat holding a row produces no session output and no tool call for `stall_after`
(default 10m), or when it is not running. Crossing an `on-unavailable` boundary SHALL require `failover_after`
consecutive unhealthy ticks (default 2). Returning to an earlier tier SHALL be manual by default.

#### Scenario: A stalled seat

- **WHEN** a builder holds a row with no output for ten minutes and the next tick confirms it
- **THEN** tier 2 becomes active, and one event names the reason `stalled`

#### Scenario: A single slow tick

- **WHEN** a seat is silent for one tick and speaks again before the next
- **THEN** nothing fails over, and the tier stays active

### Requirement: A live row is not migrated between tiers

A live row SHALL belong to the rig that accepted it. A tier change SHALL NOT move or re-hand it. A row whose seat is
unhealthy for `stall_after` SHALL be closed `superseded`, so the item is re-dispatched into the active tier on the same
branch, which `WorktreeAddOrReuse` reuses.

#### Scenario: Work in flight when Claude runs out

- **WHEN** tier 1 stalls with a row in progress and tier 2 takes over
- **THEN** the old row is closed `superseded` with the reason, and the item is re-dispatched in tier 2 on its branch
