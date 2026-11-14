## ADDED Requirements

### Requirement: The project header shows the age of both mirrors
The project surface SHALL show the age of the last successful forge read and the last successful rig read, read from
`mirror_meta`, and SHALL mark an age past twice the tick interval as quiet and past five times as stalled. Those floors
SHALL rise to the mirror's own poll window when that window is longer: a quiet box sleeps `rig_lifecycle.idle_poll_max`,
which is the freshest stamp it can write, so an age past one window is quiet and past two windows is stalled.

#### Scenario: A quiet project
- **WHEN** the last forge read is 10 minutes old, the tick interval is 60 seconds, and the poll has no backoff (no
  `rig_lifecycle` block)
- **THEN** the header shows "last forge read 10m" marked stalled

#### Scenario: A project on a backed-off poll
- **WHEN** the last forge read is 9 minutes old, the tick interval is 60 seconds, and `idle_poll_max` lets the poll
  sleep 10 minutes between reads
- **THEN** the header shows "last forge read 9m" with no state marker, because that age is still inside the window the
  mirror was told to keep

#### Scenario: No read yet
- **WHEN** a project has never completed a read
- **THEN** the header says so in words rather than showing a zero age
