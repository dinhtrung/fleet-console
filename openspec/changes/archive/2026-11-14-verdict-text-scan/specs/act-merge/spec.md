## ADDED Requirements

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
