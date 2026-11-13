## ADDED Requirements

### Requirement: An uncomputed mergeability is waiting, not a refusal

When the forge read carries no mergeability answer, the gate SHALL treat the change request as waiting: it SHALL NOT
merge, SHALL NOT raise a merge-repair decision, and SHALL NOT record a blocked merge. The next tick SHALL read the
forge again.

#### Scenario: GitHub has not scored the pull request yet

- **WHEN** a pull request reads `mergeable: null` with merge state `unknown`
- **THEN** the tick prints a waiting line, raises no decision row, records no refusal, and merges nothing

#### Scenario: The computed answer arrives

- **WHEN** a later read of the same pull request returns `mergeable: false` with state `dirty`
- **THEN** the gate refuses with the forge's word in the line and raises exactly one repair decision

#### Scenario: A computed yes still merges

- **WHEN** the read returns `mergeable: true` and the head carries an ACCEPT with green checks
- **THEN** the merge proceeds as it does today

### Requirement: The mergeability word comes from the read that computed it

When an adapter refetches a single change request because the list response left mergeability unanswered, it SHALL
carry that response's mergeability state, not the list response's.

#### Scenario: The refetch names the conflict

- **WHEN** the list page says `mergeable: null, mergeable_state: unknown` and the single read says `mergeable: false,
  mergeable_state: dirty`
- **THEN** the change request's merge state is `dirty` and `MergeConflict()` reports true

### Requirement: A conflicting head is not asked to be reviewed or approved

When an open change request closing an item conflicts with its base, the plan SHALL raise the merge-repair decision
for that head and SHALL NOT raise an approval question or a re-review for it.

#### Scenario: No verdict on a conflicting head

- **WHEN** the change request is `dirty` and no verdict names its head
- **THEN** no `decision.no_verdict` question is raised and one `decision.merge_repair` row names the branch and the
  forge's mergeability word

#### Scenario: A verdict on an older head

- **WHEN** the change request is `dirty` and the newest verdict names an older head
- **THEN** no `decision.stale` re-review is raised; the repair row is the only ask

#### Scenario: The repaired head merges

- **WHEN** the branch is rebased, its head is green and an ACCEPT names it
- **THEN** the next tick merges it and resolves the repair row

### Requirement: A repair decision does not outlive its premise

An item SHALL have at most one open merge-repair row. The row SHALL gain a note naming a later head instead of a
second row, and SHALL be resolved when the item closes, the change request closes, or a changed head merges.

#### Scenario: The item merges on a rebased head

- **WHEN** the item merges at a head newer than the one the open row names
- **THEN** the row is resolved with a note naming the merged head

#### Scenario: The head moves but the conflict remains

- **WHEN** the head changes and still conflicts with its base
- **THEN** the open row gains a note naming the new head and no second row is created

#### Scenario: A resolved row does not silence the next conflict

- **WHEN** the row was resolved and the same change request conflicts again at a later head
- **THEN** exactly one new repair row is raised
