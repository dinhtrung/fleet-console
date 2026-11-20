## ADDED Requirements

### Requirement: The board keeps the last plan reason per item
The tick SHALL record, per item, the plan's reason for leaving it undispatched, with its detail and the head it was
computed on, and SHALL delete that record in the tick the item is dispatched.

#### Scenario: Two ticks, same reason
- **WHEN** an item is skipped for `no_free_builder` on two consecutive ticks
- **THEN** one row stands, carrying the later tick's time

#### Scenario: The reason changes
- **WHEN** the same item is skipped for `live_row` after `no_free_builder`
- **THEN** the row carries `live_row` and no history is kept

#### Scenario: Dispatched
- **WHEN** the item is dispatched
- **THEN** its reason row is gone in the same tick

### Requirement: The item's why-column reads from that record
The project surface SHALL show each undispatched item's last reason and detail, and SHALL show nothing where no plan
has run since the app started.

#### Scenario: A reason from the last tick
- **WHEN** an item carries a recorded reason
- **THEN** the surface shows the reason and its detail, not a blank cell

#### Scenario: Never planned
- **WHEN** no tick has run since start
- **THEN** the cell says so in words
