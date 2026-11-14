## ADDED Requirements

### Requirement: A seat waiting on a human is visible
The project surface SHALL list every seat on the project's rigs whose lifecycle is `attention_required` or whose node
reports a pending input, and SHALL show the seat, the rig, the question text the rig reports and how long the app has
seen it.

#### Scenario: A seat blocked at restore
- **WHEN** a rig read reports a seat as `attention_required` with a question
- **THEN** the project surface lists that seat with the question and the age

#### Scenario: No question text
- **WHEN** the rig reports the seat as waiting but sends no text
- **THEN** the seat is still listed, saying the rig gave no question

#### Scenario: An unread rig hides nothing
- **WHEN** the rig read fails
- **THEN** the list is marked unknown rather than showing the seats as clear

### Requirement: One obligation per question
The control plane SHALL write one attention event and one notice for each seat and question it first sees, SHALL write
none while the same question stands, and SHALL resolve it on the first successful read that no longer reports it.

#### Scenario: The same question over several ticks
- **WHEN** five ticks read the same seat with the same question
- **THEN** one event and one notice are written

#### Scenario: Answered
- **WHEN** a successful read returns the seat without the question
- **THEN** one resolution event is written and the notice clears

#### Scenario: A failed read proves nothing
- **WHEN** the rig read fails while a question is outstanding
- **THEN** nothing is resolved

### Requirement: The capacity line names the human
When an item is not dispatched because every admitted seat is taken and at least one of those seats is waiting on a
human, the reason SHALL say how many are waiting rather than only that seats are taken.

#### Scenario: One seat waiting
- **WHEN** two admitted seats are taken and one is `attention_required`
- **THEN** the reason reads `every admitted seat is taken (1 waiting on a human)`
