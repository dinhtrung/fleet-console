# dispatch Specification

## Purpose
Which open forge items may be handed to a seat, and in what order: one child of an epic in flight at
a time, a dead end parked after one question per head, an operator release that re-opens it, and the labels that keep
owner-only work out of the queue.

## Requirements

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

#### Scenario: A silent holder
- **WHEN** the holding child has a live dispatch whose seat has gone silent for hours
- **THEN** the epic stays held, and the silent-dispatch question tells a human rather than a second builder starting

#### Scenario: A draft change request
- **WHEN** the holding child's change request is open, unmerged and still a draft
- **THEN** the next sibling is not dispatched

#### Scenario: Two siblings already in flight when the rule lands
- **WHEN** an epic already has two children dispatched before this rule is deployed
- **THEN** neither dispatch is cancelled, and no third child is dispatched until both land

### Requirement: An unchanged dead end is not re-dispatched

When an item's newest dispatch ended without a change request and the branch head origin reports is still the
head that dispatch stopped at, the plan SHALL skip the item with the reason `dead_end` and SHALL NOT adopt the
branch as unfinished work. When the head has moved since the dispatch ended, or the item has never been
dispatched, the branch SHALL be adopted exactly as before.

#### Scenario: The attempt ended on the head origin still reports

- **WHEN** the newest dispatch for an open item ended without a change request at head `H`, the item's branch
  still has no change request, and origin still reports `H`
- **THEN** the plan dispatches nothing for the item and skips it with `dead_end` naming the branch and `H`

#### Scenario: Nobody pushed since

- **WHEN** the next tick reads the same item at the same head
- **THEN** the plan dispatches nothing and raises no second question

#### Scenario: The branch moved

- **WHEN** origin reports a head other than the one the attempt stopped at
- **THEN** the plan adopts the branch and dispatches the item

#### Scenario: Never dispatched

- **WHEN** an item has a branch with no change request and no dispatch attempt
- **THEN** the plan adopts the branch and dispatches the item, as before the rule existed

### Requirement: A dead end asks the operator once per head

The plan SHALL raise exactly one operator question per `(item, branch head)`: what stopped, the branch and the
head, and the three answers — release, hold, or hand it to a person — with the last row's resolution when the
queue read carries one. A repeated tick SHALL add no second question, and a moved head SHALL ask again.

#### Scenario: One question over several ticks

- **WHEN** five ticks read the same dead end at the same head
- **THEN** one queue row asks the operator, and the four later ticks add none

### Requirement: An operator release makes the item eligible again

The operator SHALL be able to release a dead end by answering its question with `release`; the next plan SHALL
dispatch the item at the unchanged head. The release SHALL apply only to the attempt it answered: a dispatch
started after the answer spends it, so the same head parking again is a new dead end.

#### Scenario: Release answered after the end

- **WHEN** the operator answers the dead-end question with `release` after the attempt ended
- **THEN** the next plan adopts the branch and dispatches the item

#### Scenario: The release is spent

- **WHEN** a later attempt ends at the same head after the release was answered
- **THEN** the item parks again instead of re-dispatching on the old answer

### Requirement: A manual label keeps owner-only tasks out of dispatch

A project SHALL be able to name a label, default `manual`, and an item carrying it SHALL be skipped like
`onhold` with its own reason, so an epic can mark owner-only tasks (`[live]` checks, post-deploy steps) at
creation time. The board SHALL show such items as manual, and an opted-in serial epic SHALL not count a
`manual` child as holding its slot.

#### Scenario: A manual task is never dispatched

- **WHEN** an open item carries the `manual` label
- **THEN** the plan skips it with reason `manual` and dispatches nothing for it
