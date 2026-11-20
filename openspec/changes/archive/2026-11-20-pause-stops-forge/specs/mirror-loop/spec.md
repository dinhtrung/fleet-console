## ADDED Requirements

### Requirement: A paused project is not read

The read pass SHALL skip a paused project's forge and git reads, SHALL leave its item, change-request and branch
snapshots and its forge stamp untouched, and SHALL still record the fleet-wide queue and roster for its rigs.

#### Scenario: A paused tick

- **WHEN** a tick runs while a project is paused
- **THEN** its forge and git are not called, its forge snapshot and stamp do not change, and its rig rows stay
  current

#### Scenario: The first tick after resume

- **WHEN** the project is resumed
- **THEN** the next tick reads the forge again and advances the stamp
