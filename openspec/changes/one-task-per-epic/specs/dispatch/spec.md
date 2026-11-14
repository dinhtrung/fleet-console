## ADDED Requirements

### Requirement: One child of an epic is in flight at a time
A project SHALL dispatch at most one open child of an epic at any time. A child SHALL be eligible only when no sibling
of the same epic holds a live dispatch or an open, unmerged change request; among eligible siblings the lowest number
SHALL be taken first.

#### Scenario: Two children, two free seats
- **WHEN** an epic has two ready children and the project has two free seats
- **THEN** one child is dispatched and the other is skipped with `epic_busy` naming the child in flight

#### Scenario: The holder lands
- **WHEN** the holding child's change request merges
- **THEN** the next tick dispatches the next sibling, in number order

#### Scenario: A dispatch that ends without work
- **WHEN** the holding child's dispatch ends and no change request is open
- **THEN** the epic holds nothing and the next sibling is eligible on the next tick

#### Scenario: An open change request without a live dispatch
- **WHEN** the holding child has an open unmerged change request and no live dispatch
- **THEN** the next sibling is still not dispatched, and the skip names the holder as in review

#### Scenario: A sibling on hold
- **WHEN** a sibling carries `onhold`
- **THEN** it holds the epic for nobody, and the other siblings proceed by this rule

#### Scenario: An item without a parent
- **WHEN** two items have no epic parent
- **THEN** both are dispatched as today, up to the concurrency cap

### Requirement: The rule can be turned off per project
A project SHALL be able to set `epic_serial: false` to restore parallel dispatch of siblings, and the setting SHALL be
visible in `--print-config`.

#### Scenario: An epic of independent chores
- **WHEN** a project sets `epic_serial: false`
- **THEN** every eligible sibling is dispatched as before, and the setting is shown by `--print-config`
