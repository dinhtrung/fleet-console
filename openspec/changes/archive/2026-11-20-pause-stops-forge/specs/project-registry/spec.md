## ADDED Requirements

### Requirement: A paused project is not a forge client

A project whose row is `onhold`, or `enabled: false`, SHALL produce no forge traffic: no item or change-request
read, no note read, no verdict capture, no merge, no note write, no branch read. Resume SHALL make the next tick
read and reconcile as usual.

#### Scenario: Paused from the board

- **WHEN** the operator pauses a project
- **THEN** every later tick runs no forge command for it and logs no `mirror:` or `act:` line for it

#### Scenario: Resume

- **WHEN** the operator resumes it
- **THEN** the wake makes the next tick read the forge and reconcile what happened while it was paused

#### Scenario: A rig whose projects are all paused

- **WHEN** every project on a managed rig is paused
- **THEN** the idle rule may still stop that rig, because a rig stop is not forge traffic
