## ADDED Requirements

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
