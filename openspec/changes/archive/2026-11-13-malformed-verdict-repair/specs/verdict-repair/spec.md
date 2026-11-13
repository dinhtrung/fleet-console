# verdict-repair — from a note that claims a verdict to a human who can act

## ADDED Requirements

### Requirement: A malformed verdict is recorded once

A note whose first line starts with `ACCEPT`/`REJECT` after decoration is stripped and whose sha does not parse
SHALL write `verdict.malformed` once per note, and its journal line SHALL NOT repeat on later ticks.

#### Scenario: The same bad note over two ticks

- **WHEN** a reviewer posts `## QA Verdict: ACCEPT ✅` and two ticks run without a new note
- **THEN** the first tick prints the refusal and writes one audit row, and the second prints no line and writes no row

#### Scenario: Two different bad notes

- **WHEN** the reviewer posts a second malformed note
- **THEN** it is a new fact: one new row, one new line, and the question is asked again

### Requirement: The reviewer is asked, and told the exact line

For a malformed note the app SHALL raise one question to the reviewer seat per (note, head) whose brief and
change-request comment carry the literal line to post, and SHALL NOT also raise `decision.no_verdict` for that head.

#### Scenario: The reviewer repairs it

- **WHEN** the reviewer posts a comment whose first line is `ACCEPT <head>`
- **THEN** the verdict is captured, the question closes, and the merge proceeds on the gate's own facts

#### Scenario: A live question already names the head

- **WHEN** a question row for this (item, head) is still live
- **THEN** no second question is raised, and the existing nudge path is what re-asks

### Requirement: The operator is asked only after a deadline

When a malformed-verdict question has stood for `StaleEscalateAfter` (45 minutes, two nudge cycles), the app SHALL
raise one `decision.stall` to `human@kernel` naming the note, its line and the head, and SHALL offer two lawful
moves: cast the verdict on the item drawer, or instruct the reviewer.

#### Scenario: The deadline passes

- **WHEN** the malformed row is older than 45 minutes with no parseable verdict
- **THEN** exactly one operator question exists for that (item, head), and it names the note and the line

#### Scenario: The operator answers

- **WHEN** a verdict is cast on the item drawer for the head
- **THEN** the gate merges on the next tick, with no hand merge and no second dispatch

### Requirement: A note that names no commit never merges

The parser SHALL NOT attribute a note without a parseable sha to the head under review, and no deadline SHALL be
read as permission to merge without a verdict naming the head.

#### Scenario: An emoji instead of a sha

- **WHEN** the first line reads `ACCEPT ✅`
- **THEN** the merge is refused and the only repairs are a new note or a verdict cast by a human
