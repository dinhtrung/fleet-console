# act-merge Specification

## Purpose

How the merge gate and the repair ask behave when a head cannot merge. The gate treats an uncomputed
mergeability as waiting, never as a refusal; a conflicting head is a planning input, so the review and the
approval question are never spent on a head that cannot merge; and the one repair row an item holds does not
outlive the premise that raised it — it is noted per head and resolved when the item closes, the change request
closes or merges, or the head it conflicts with is the head that lands. The requirements here were proven on
epic #341's own incident: PR #335 spent a full review on a head that could never merge, and its repair row
outlived two heads.

## Requirements

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

### Requirement: A wrapped verdict is read as a verdict
The gate SHALL capture a verdict whose token opens a line once fences, links, emphasis, decoration, icons and one
label from the closed list (`QA`, `Verdict`, `Review`, `Result`, `Decision`) are stripped.

#### Scenario: The prefix that broke the gate
- **WHEN** the newest note is `QA ACCEPT 930297d9…` and 930297d9… is the head
- **THEN** the verdict is captured for that head and the merge proceeds

#### Scenario: Decoration around the token
- **WHEN** the note is `✅ **ACCEPT <head>**` or `[ACCEPT](https://…) <head>` or `## QA Verdict: ACCEPT <head>`
- **THEN** the verdict is captured for the head

#### Scenario: The sha sits on the next line
- **WHEN** the note is `Verdict:` then `ACCEPT` then the head on its own line
- **THEN** the verdict is captured for the head

### Requirement: Prose is not a verdict
A verdict word that does not open a line SHALL NOT be read as a verdict, and SHALL NOT raise a question.

#### Scenario: A sentence about a verdict
- **WHEN** the newest note is `Not ready to ACCEPT <head> until CI is green`
- **THEN** no verdict is stored, no merge happens, and no question is raised

### Requirement: A short sha names the head only as its prefix
A sha of seven or more characters SHALL be attributed as today. A sha of four to six characters SHALL be captured
only when it is a prefix of the head under review, and the row SHALL store the head. Any other short sha SHALL be
skipped.

#### Scenario: An abbreviation of the head
- **WHEN** the note is `ACCEPT 930297d` and the head is `930297d9ac33…`
- **THEN** the verdict is captured and stored against the head

#### Scenario: An abbreviation of nothing
- **WHEN** the note is `ACCEPT ffff` and the head is `930297d9…`
- **THEN** no verdict is stored and no question is raised

### Requirement: An unattributable note asks the operator once
A note that states a verdict the gate cannot attribute — no sha, both polarities, or a negation — SHALL raise one
row to the project's operator destination, and the gate SHALL NOT merge on it.

#### Scenario: The three ambiguous shapes
- **WHEN** the newest note is `ACCEPT`, `ACCEPT <head>` followed by `REJECT <head>`, or `Verdict: not ACCEPT <head>`
- **THEN** one question row is created and no verdict is stored

#### Scenario: One question, many ticks
- **WHEN** the same ambiguous note is seen on five consecutive ticks at the same head
- **THEN** exactly one question row exists

#### Scenario: The destination
- **WHEN** the project sets `verdict_relay.mode: operator` with a seat, or leaves the block absent
- **THEN** the question goes to that seat, or to `human@kernel`

#### Scenario: The seat cannot be reached
- **WHEN** the row cannot be created for the configured seat
- **THEN** the question is created once for `human@kernel` instead

### Requirement: A verdict about another commit never moves to the head on its own
A verdict whose sha is not the head SHALL NOT merge and SHALL NOT be moved to the head. When the sha is not in the
head's history the tick SHALL ask the operator destination to accept at the head, reject, or re-review; when it is an
ancestor of the head, the stale verdict is the reviewer's question (see `review-verdict`) and the tick SHALL NOT open
an operator question.

#### Scenario: An acceptance of the parent commit
- **WHEN** the newest note is `ACCEPT <parent>` and the head is `<head>`
- **THEN** no verdict is stored for the head and no operator question is raised; the reviewer is asked once per head
  through the review path
