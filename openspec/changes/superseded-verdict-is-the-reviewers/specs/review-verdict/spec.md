## ADDED Requirements

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
