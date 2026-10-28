## ADDED Requirements

### Requirement: A version floor is checked at boot

The app SHALL read `rig --version` at boot and refuse to start its act loop when the version is below the declared
floor, while the mirror and board continue to run.

#### Scenario: An old rig on a new box

- **WHEN** the box runs a rig older than the floor
- **THEN** the board shows the compat refusal with both versions and nothing is dispatched

### Requirement: Required fields are strict, unknown keys are tolerated

A decoded rig response SHALL fail when a required field is absent or empty, and SHALL NOT fail when an unknown field
appears. Each unknown key SHALL be logged once per version.

#### Scenario: A renamed field

- **WHEN** `pickup_state` becomes `pickupState`
- **THEN** the compat probe fails naming the missing field, and the act loop stays off

#### Scenario: An added field

- **WHEN** a rig release adds a field the app does not use
- **THEN** reads still succeed and the key is logged once

### Requirement: One adapter per command, no raw shapes downstream

Each rig CLI command SHALL have one adapter function returning a domain type. No decoded map or raw JSON SHALL cross
the adapter package boundary.

#### Scenario: Reviewing a new read

- **WHEN** a new rig read is added
- **THEN** it has its own adapter function and a fixture test, and the board consumes a domain type

### Requirement: Fixtures are recorded per rig version

The repository SHALL keep one recorded response per command per supported rig version, and a rig upgrade SHALL
capture new fixtures and diff them before the floor moves.

#### Scenario: Upgrading the floor

- **WHEN** the floor moves from 0.7.0 to 0.7.4
- **THEN** the fixture diff is committed with the upgrade, and every parse test passes against both sets

### Requirement: A compat failure is visible, never silent

A compat failure SHALL appear on the board and in `GET /api/compat` with the command, the missing field and the
observed version.

#### Scenario: The daemon is up but the shape changed

- **WHEN** the queue read fails its required-field check
- **THEN** every project reads `compat-blocked` with that command named, instead of an empty queue
