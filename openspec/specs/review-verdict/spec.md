# review-verdict Specification

## Purpose
What a review note must look like to count as a verdict, and what the control plane does when a review ends without one.

## Requirements

### Requirement: A verdict note is recognised through markdown decoration
The capture SHALL strip leading decoration — `#`, `>`, list bullets, backticks, `**`, `__`, `_` — and one optional `:` or dash separator before matching a verdict token and a sha on the first non-empty line. The sha SHALL still have to name the head under review.
#### Scenario: A bold verdict
- **WHEN** a reviewer posts a note whose first line is `**ACCEPT 3f2a1b9c**` and that sha is the head
- **THEN** the verdict is captured as `accept` for `3f2a1b9c`
#### Scenario: A heading verdict
- **WHEN** a reviewer posts `## Verdict: ACCEPT 3f2a1b9c` and the sha is the head
- **THEN** the verdict is captured, not skipped
#### Scenario: A colon separator
- **WHEN** a reviewer posts `ACCEPT: 3f2a1b9c` and the sha is the head
- **THEN** the verdict is captured instead of refused

### Requirement: A sentence is not a verdict
A first line that merely uses a verdict word SHALL NOT be captured, and a line that claims a verdict with no parseable sha SHALL be refused rather than skipped.
#### Scenario: Prose that mentions a token
- **WHEN** the first line is `Not ready to ACCEPT 3f2a1b9c until CI is green`
- **THEN** no verdict is captured and no refusal is recorded
#### Scenario: The sha does not parse
- **WHEN** a note reads `ACCEPT` on its own line for an open change request
- **THEN** the capture refuses with an unattributable-verdict error naming the note

### Requirement: A missing verdict becomes an operator question
When a change request is open, its item is open, no reviewer row is live and no verdict names the head, the app SHALL raise exactly one operator question per `(item, head sha)`, naming the board's Approve/Reject and the item's reply line.
#### Scenario: The reviewer seat ends its row with no note
- **WHEN** a review row ends and the change request carries no verdict for its head
- **THEN** exactly one queue row with destination `human@kernel` is created
- **AND** no reviewer row is created for that item
#### Scenario: The same head is planned again
- **WHEN** the next tick plans the same item and the head has not moved
- **THEN** no second operator question is raised
#### Scenario: The head moves
- **WHEN** the head changes and there is still no verdict for the new head
- **THEN** one new operator question is raised, naming the new sha

### Requirement: A stale verdict asks the reviewer once per head
A parsed verdict whose sha is not the head SHALL raise one reviewer row per `(item, head sha)`, and none when the head has not moved.
#### Scenario: A verdict on an older head
- **WHEN** an `ACCEPT` names `aaaa111` and the head is `bbbb222`
- **THEN** one reviewer row is created asking for a review of `bbbb222`
#### Scenario: The head has not moved
- **WHEN** the same stale verdict is planned again at the same head
- **THEN** no second reviewer row is created

### Requirement: The review brief carries the head and the command
The brief a reviewer receives SHALL name the change request number and the head sha verbatim, and SHALL contain the literal command that posts the verdict note with the sha substituted.
#### Scenario: A reviewer opens its brief
- **WHEN** a reviewer row is raised for change request 653 at head `3f2a1b9c`
- **THEN** the brief contains `653`, `3f2a1b9c`, and a command whose body starts `ACCEPT 3f2a1b9c`

### Requirement: The change-request note stays the only verdict record
A missing verdict SHALL NOT be answered by reading a queue row, a handoff summary or a seat's claim as a verdict; the note on the change request remains the record the merge gate reads.
#### Scenario: A row resolution says accept without a note
- **WHEN** a review row's resolution reads `ACCEPT 3f2a1b9c` and the change request carries no such note
- **THEN** no verdict is captured and the operator question is raised

### Requirement: A verdict on a superseded commit does not ask the operator
A verdict note whose sha resolves to an ancestor of the change request's head SHALL NOT raise an operator verdict question, SHALL NOT be stored as a verdict for the head, and SHALL leave the merge of the head refused until a verdict names the head.

#### Scenario: The reviewer rejected the previous head and the builder pushed a fix
- **WHEN** the newest verdict note is `REJECT aaaa111`, `aaaa111` is an ancestor of the head `bbbb222`, and a review row for `bbbb222` is live
- **THEN** no operator verdict question is raised
- **AND** `bbbb222` is not merged
- **AND** one skip line names the note and the head

#### Scenario: An accept on an older commit
- **WHEN** the newest verdict note is `ACCEPT aaaa111` and `aaaa111` is an ancestor of the head `bbbb222`
- **THEN** no operator verdict question is raised and `bbbb222` is not merged

#### Scenario: The head gets its own verdict
- **WHEN** a later note `ACCEPT bbbb222` names the head
- **THEN** the head merges as for any accepted head

### Requirement: A verdict outside the head's history still asks the operator
A verdict note whose sha is not an ancestor of the head, or whose ancestry cannot be established, SHALL raise an operator verdict question as before.

#### Scenario: A sha from another branch
- **WHEN** the newest verdict note is `REJECT cccc333` and `cccc333` is not in the head's history
- **THEN** one operator verdict question is raised for `(note, head)`

#### Scenario: The ancestry check fails
- **WHEN** the repository cannot answer whether the note's sha is an ancestor of the head
- **THEN** one operator verdict question is raised for `(note, head)`

### Requirement: Standing questions for a superseded head close when the head has a verdict
When a verdict for the change request's current head is captured, standing operator verdict questions for the same item whose head differs SHALL be closed as superseded, naming the captured verdict; questions about the current head SHALL stay open.

#### Scenario: A question about the old head
- **WHEN** a question for head `aaaa111` is standing and `ACCEPT bbbb222` is captured for head `bbbb222`
- **THEN** the question row is closed `superseded` with a note naming `ACCEPT bbbb222`

#### Scenario: A repeat tick
- **WHEN** the next tick runs with nothing new
- **THEN** no further rows are closed
