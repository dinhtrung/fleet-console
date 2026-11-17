## ADDED Requirements

### Requirement: A paused project's header says paused

The project header SHALL render a paused project's forge read as `paused — no forge reads` in state `paused`,
whatever the stamp's age, instead of the age-based quiet or stalled state.

#### Scenario: A day-long pause

- **WHEN** a project has been paused for a day and its forge stamp is a day old
- **THEN** the header says paused, not stalled

#### Scenario: Resume resets the clock

- **WHEN** the project is resumed and the next tick reads the forge
- **THEN** the header shows the fresh age and its normal state again
