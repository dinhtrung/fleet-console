# act-tick — the tick's contract with the process

## ADDED Requirements

### Requirement: A tick planned without a clock still runs

`RunTick` SHALL treat an absent `TickInput.Now` as the wall clock, and no code SHALL call the field directly.
A silent-dispatch intent over a live dispatch row with a parseable `CreatedAt` SHALL produce its raise, not a
nil call.

#### Scenario: The production wiring omits the clock

- **WHEN** `RunTick` receives a `TickInput` whose `Now` is nil, exactly as `cmd/fleet-console` builds it, and the
  plan carries a `dispatch.silent` intent for a live dispatch row older than the threshold
- **THEN** the tick returns a report holding the raised question, and no panic reaches the caller

#### Scenario: The age cannot be read

- **WHEN** the dispatch row's `CreatedAt` does not parse under the store's layouts
- **THEN** the tick skips that item with "the dispatch's age cannot be read" and touches nothing

### Requirement: A panicking tick does not end the loop

`Mirror.Loop` SHALL recover a panic raised by any part of one tick, SHALL report that tick as busy, and SHALL
run the next tick. The process SHALL NOT exit because of a panic inside a tick.

#### Scenario: One bad tick, then a good one

- **WHEN** a tick panics
- **THEN** the loop logs the panic, waits the base interval, and the following tick runs normally

#### Scenario: The context ends while limping

- **WHEN** the context is cancelled after a recovered panic
- **THEN** `Loop` returns without another tick and without re-panicking

### Requirement: A recovered panic is one readable journal line

A recovered panic SHALL be logged as `tick: panicked: <value>` followed by the stack, and SHALL NOT create an
audit row, a queue row, or a decision.

#### Scenario: The operator greps the journal

- **WHEN** a tick panics
- **THEN** the journal carries one line starting `tick: panicked:` whose stack names the failing site, and no
  new queue item or decision exists for it
