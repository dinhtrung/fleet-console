## ADDED Requirements

### Requirement: A project names one or more rigs, in tiers

A project SHALL name its rigs as an ordered list of tiers. Each entry SHALL name one rig and its spill trigger,
`on-full` or `on-unavailable`. The one-element form `rig: name` SHALL remain valid. An empty list SHALL fail
validation, and a rig named twice SHALL be rejected at load.

#### Scenario: Two rigs in one tier

- **WHEN** a project lists `a` then `b` in tier 1 and `a` has no free builder
- **THEN** the next dispatch goes to `b`, and both items land in the same tier

#### Scenario: A standby tier that is not touched under load

- **WHEN** tier 1 is full but every seat in it is healthy, and tier 2 is `on-unavailable`
- **THEN** nothing is dispatched to tier 2 and the item waits for tier 1

#### Scenario: An empty list

- **WHEN** a project sets `rigs: []`
- **THEN** config validation fails naming the project, and nothing is applied

### Requirement: A project that leaves the file is disabled

Applying a config SHALL set `enabled = 0` on every project it does not name and SHALL report `disabled <key>`. Its rows
SHALL survive. `--prune` SHALL be the only deletion path, and SHALL refuse while a dispatch for that project is live. A
project that reappears SHALL be re-enabled with its `onhold` value intact.

#### Scenario: Removing a project block

- **WHEN** a project is deleted from the file and the config is applied
- **THEN** its row is disabled, the next plan has no intent for it, and its history still reads

#### Scenario: Pruning a live project

- **WHEN** `--prune` is asked for a project with an open dispatch
- **THEN** it is refused, naming the row, and nothing is deleted

## MODIFIED Requirements

### Requirement: Capacity comes from the rig

Free builders SHALL be computed from the rigs of the project's active tier — each rig's seats minus rows that are
working, claimed or in progress — summed across that tier and capped by the project's `concurrency_cap`. Capacity SHALL
NOT be a configured number. A rig whose seats cannot be read SHALL contribute zero and SHALL be reported per project.

#### Scenario: A seat parked on a prompt is not free

- **WHEN** a builder's seat reports `needs_input` on a permission prompt
- **THEN** it counts as busy and a decision is raised, because no tick can clear a menu

#### Scenario: One unreadable rig does not blank the project

- **WHEN** one rig of the tier cannot be read and another has two free builders
- **THEN** capacity is two, the unreadable rig is reported on the board, and the other rig dispatches
