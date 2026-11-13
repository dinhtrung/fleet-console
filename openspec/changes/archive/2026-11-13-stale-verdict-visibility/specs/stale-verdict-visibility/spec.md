## ADDED Requirements

### Requirement: An unattributable verdict still never merges
A verdict whose sha is not the head under review SHALL end the scan without storing a verdict, and the merge gate
SHALL NOT merge on the strength of any older note.

#### Scenario: A rejection on the parent commit
- **WHEN** the newest verdict-shaped note is `REJECT a7463125…` and the head under review is `5690552…`
- **THEN** no verdict is stored for `5690552…`, no merge happens, and the item waits

#### Scenario: An accept for another sha does not fall through
- **WHEN** the newest note is `ACCEPT a7463125…` and the head is `5690552…`
- **THEN** the scan ends with an unattributed accept and an older ACCEPT naming the head is not resurfaced

### Requirement: One record per unattributable verdict
An unattributable verdict SHALL write exactly one `verdict.unattributed` event per `(item, note)`, with the detail
grammar `note=<id>; sha=<written>; head=<head>` and the dedupe key `note=<id>;`, for a rejection exactly as for an
accept.

#### Scenario: Five ticks over one refused note
- **WHEN** the same rejection note is seen on five consecutive ticks at the same head
- **THEN** exactly one event row exists and exactly one journal line was printed

#### Scenario: A second note on the same head
- **WHEN** the reviewer posts a second unattributable note for the same item and head
- **THEN** a second event row and a second line appear, because the note is the key

#### Scenario: The ledger cannot be written
- **WHEN** the event write fails
- **THEN** the tick still prints the line, continues, and never merges or crashes

### Requirement: The refusal explains the order and the unblock
The rejection's refusal text SHALL state that the note is older than the head, SHALL name the sha the note wrote and
the head under review, and SHALL say that a verdict naming the head unblocks the merge. It SHALL NOT state or imply a
commit time.

#### Scenario: What the operator reads
- **WHEN** a tick refuses `REJECT a7463125…` at head `5690552…`
- **THEN** the line names both shas and says a verdict naming `5690552…` unblocks the merge

#### Scenario: No invented timestamps
- **WHEN** the refusal is rendered
- **THEN** it contains no time, because `forge.ChangeRequest` carries none

### Requirement: A stalled review asks a human once per head
A live stale-review row that names the current head, is older than `StaleEscalateAfter`, and has been nudged at least
once SHALL raise one `decision.stall` question to the operator per `(item, head)`.

#### Scenario: Two nudges and no answer
- **WHEN** a `decision.stale` row for head `5690552…` is 45 minutes old and has `LastNudgeAt` set
- **THEN** exactly one operator question is raised for `(#455, 5690552…)`

#### Scenario: The same head is planned again
- **WHEN** the next ten ticks see the same unanswered row at the same head
- **THEN** no second question is raised

#### Scenario: The head moves
- **WHEN** the builder pushes a new head while the row is still unanswered
- **THEN** the new head is a new question, and the old one is not repeated

#### Scenario: Answered before the threshold
- **WHEN** the row is answered at 10 minutes, or has never been nudged
- **THEN** no operator question is raised

### Requirement: A change request that closes no item is visible
A change request whose body closes no item SHALL be skipped as today and SHALL print one journal line and write one
`change_request.no_item` event per `(change request, head)`. When the title or the branch names an item, the line and
the event SHALL name that item, as a diagnostic only.

#### Scenario: The #683 shape
- **WHEN** the body says `Refs #448`, the title says `Draft: tsim #448: …` and the branch is `feature/448-…`
- **THEN** one line names `#683` and `#448`, one event exists for the head, and nothing is merged or undrafted

#### Scenario: Ten ticks at one head
- **WHEN** the same change request and head are seen on ten consecutive ticks
- **THEN** exactly one line and one event exist

#### Scenario: Nothing names an item anywhere
- **WHEN** the body, title and branch name no item
- **THEN** one line names the change request and no item is guessed

### Requirement: The title and the branch are never an item key
No part of the merge path SHALL resolve an item from a change request's title or branch, and `forge.ChangeRequest`'s
new `Title` field SHALL be read only by the diagnostic of the requirement above.

#### Scenario: A Refs change request with a matching branch
- **WHEN** a change request on `feature/448-…` closes no item
- **THEN** it is not merged, no verdict is captured from its notes, and no item is closed

### Requirement: The brief states the closing rule and the partial move
The dispatch brief's `## Publish` section SHALL state that the merge gate finds the item only through
`Closes #<item>` in the body, and SHALL tell a builder whose attempt is partial to hand off with the candidate sha
and what remains instead of opening a change request that only references the item.

#### Scenario: A rendered brief
- **WHEN** a brief is rendered for item `#448`
- **THEN** `## Publish` contains `Closes #448` and the partial-attempt sentence

#### Scenario: The finished case is unchanged
- **WHEN** a brief is rendered for any item
- **THEN** it still asks for one draft change request, no merge, and a hand-off with the sha, the commands and the
  limits
