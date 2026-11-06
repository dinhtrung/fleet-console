## ADDED Requirements

### Requirement: An idle fleet is polled less often

While a tick finds nothing — no open item, no open change request, no non-terminal queue row, and no stale or
failed forge read in any project — the app SHALL double the wait after each such tick, capped at
`rig_lifecycle.idle_poll_max`. The first tick that finds any of them SHALL return the wait to `tick_interval`.

#### Scenario: Quiet ticks double the wait

- **WHEN** four consecutive ticks find nothing anywhere
- **THEN** the waits that follow them are `tick_interval`, twice that, four times that, and eight times that, in
  that order

#### Scenario: The ceiling holds

- **WHEN** the doubling reaches `idle_poll_max`
- **THEN** every later quiet tick waits exactly `idle_poll_max`, never more

#### Scenario: A new issue restores the cadence

- **WHEN** a tick finds one open issue after a quiet stretch
- **THEN** the wait after that tick is `tick_interval`, and a second finding keeps it there

#### Scenario: A broken read is not idleness

- **WHEN** a project's forge read fails or is stale
- **THEN** the tick counts as busy, so the wait stays at `tick_interval` and the read is retried at the normal rate

### Requirement: The backoff is visible in the log

Each change of the wait SHALL write one log line naming the new wait and the reason, so an operator can tell a
slow poll from a hung loop. An unchanged wait SHALL write nothing.

#### Scenario: Backing off

- **WHEN** a quiet tick changes the wait from 1m to 2m
- **THEN** one line names the new wait and the count of consecutive quiet ticks, and the next quiet tick logs the
  change to 4m rather than repeating 2m

#### Scenario: Resetting

- **WHEN** a busy tick resets the wait to `tick_interval`
- **THEN** one line names the fact that ended the quiet stretch

### Requirement: One place configures the cadence

`tick_interval` SHALL remain the busy cadence and `rig_lifecycle.idle_poll_max` SHALL be its ceiling.
`--print-config` SHALL show both with their sources. A ceiling below `tick_interval` SHALL be refused at load with
both values named, not silently clamped.

#### Scenario: The operator reads both numbers

- **WHEN** `--print-config` runs
- **THEN** `tick_interval` and `idle_poll_max` appear with the file, environment or default they came from

#### Scenario: The ceiling is smaller than the base

- **WHEN** the configuration sets `idle_poll_max` below `tick_interval`
- **THEN** the process refuses to start and names both values and the key that must change
