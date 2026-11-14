# verdict-relay Specification

## Purpose
Let a project hand a finished review that carries no verdict to a seat that reads it, so the reviewer's words become a
verdict without a human relaying them. Off unless a project opts in; a relayed verdict is marked with the seat that
wrote it, one relay per item and head, and an inconclusive review escalates instead of deciding.

## Requirements

### Requirement: The relay is off unless configured
A project without a `verdict_relay` block SHALL behave exactly as today: no relay rows, no new intents, and every
operator question still addressed to `human@kernel`.

#### Scenario: A project that never opted in
- **WHEN** a review ends without a verdict on a project with no `verdict_relay` block
- **THEN** no relay row is created and the tick behaves as it does before this change

### Requirement: A finished review without a verdict is relayed
When an open change request has no verdict at its head and a queue row naming that head, addressed to the reviewer
seat, has ended, the app SHALL plan one relay for that head.

#### Scenario: The reviewer ended its row without a verdict
- **WHEN** the reviewer row for head `abc1234` is done and no verdict names that head
- **THEN** the plan carries one relay intent for item and head `abc1234`

#### Scenario: The reviewer is still working
- **WHEN** the reviewer row for that head is pending or in progress
- **THEN** no relay is planned

### Requirement: A verdict at the head needs no relay
A stored verdict whose sha is the head — full or an abbreviation of it — SHALL suppress the relay for that head.

#### Scenario: The reviewer wrote the verdict
- **WHEN** a verdict row names the head that the change request carries
- **THEN** no relay is planned for it

### Requirement: The brief carries the review verbatim and the command
The relay's brief SHALL contain every note body verbatim with its note id, the full 40-character head sha, the item
and change request numbers, the exact command that posts the verdict, and the `via:` line it must add.

#### Scenario: A prose review reaches the relay
- **WHEN** the change request carries two comments, one of them prose approval
- **THEN** the brief quotes both bodies in full and names the head sha the gate will judge

### Requirement: The relay writes one note and never merges
The relay SHALL be told to post exactly one note in the verdict grammar, and SHALL NOT be asked to merge, close the
change request, edit code or touch the queue.

#### Scenario: The relay answers
- **WHEN** the relay posts `ACCEPT <head>`
- **THEN** the merge still happens through the gate's own four facts on a later tick

### Requirement: A relayed verdict is marked and the marker is stored
A relayed note SHALL carry `via: <seat>` as its second non-empty line, and capture SHALL store that marker in the
`verdict.capture` event detail.

#### Scenario: Capture reads a relayed note
- **WHEN** the note's second line is `via: verdict-scribe@my-rig`
- **THEN** the capture event detail carries `via=verdict-scribe@my-rig`

#### Scenario: A reviewer's own note
- **WHEN** the note has no `via:` line
- **THEN** the capture detail carries no marker and the verdict reads as the reviewer's own

### Requirement: An inconclusive review escalates, it does not decide
When the review does not decide, the relay SHALL answer its row with a sentence naming what it read and SHALL write
nothing on the forge, and the app SHALL then raise the operator question for that head.

#### Scenario: Notes that hedge
- **WHEN** the relay finds only a question and no approval or objection
- **THEN** it answers the row, writes no note, and the operator question is raised for that head

### Requirement: One relay per item and head
The app SHALL record one `verdict.relayed` event per (item, head) with `head=<sha>;` as its dedupe key, and SHALL NOT
relay the same head twice.

#### Scenario: Two ticks over one head
- **WHEN** two consecutive ticks see the same verdict-less head
- **THEN** exactly one relay row exists and one `verdict.relayed` event is recorded

### Requirement: Operator mode addresses questions to the configured seat
With `mode: operator`, an operator question SHALL be addressed to `verdict_relay.seat` instead of `human@kernel`, and
with `mode: scribe` questions SHALL keep going to `human@kernel`.

#### Scenario: A box whose operator is an agent
- **WHEN** the project sets `mode: operator` and `seat: operator-agent@kernel`
- **THEN** the raised question row's destination is `operator-agent@kernel`

### Requirement: The block is validated at load and visible in print-config
A `verdict_relay` block with an unknown mode or a mode and no seat SHALL be refused by the loader with a field
error, and `--print-config` SHALL show the effective mode and seat.

#### Scenario: A mode without a seat
- **WHEN** the file sets `mode: scribe` and no `seat`
- **THEN** the loader answers a field error naming `verdict_relay.seat` and the service does not start
