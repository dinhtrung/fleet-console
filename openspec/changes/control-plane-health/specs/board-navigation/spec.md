## ADDED Requirements

### Requirement: The project header shows the age of both mirrors
The project surface SHALL show the age of the last successful forge read and the last successful rig read, read from
`mirror_meta`, and SHALL mark an age past twice the tick interval as quiet and past five times as stalled.

#### Scenario: A quiet project
- **WHEN** the last forge read is 10 minutes old and the tick interval is 60 seconds
- **THEN** the header shows "last forge read 10m" marked stalled

#### Scenario: No read yet
- **WHEN** a project has never completed a read
- **THEN** the header says so in words rather than showing a zero age
