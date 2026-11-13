# mirror-loop Specification

## Purpose
The loop that keeps the database a faithful copy of the forge and the rig: cadence, idle backoff, and the wake that ends a wait when something changes.

## Requirements

### Requirement: A board write asks the mirror loop for a tick

A successful board write that changes what the next tick would read SHALL ask the mirror loop to tick now, instead of
waiting out the idle backoff.

#### Scenario: A project is created on the board

- **WHEN** `POST /api/projects` writes the config entry and the project row
- **THEN** the mirror loop is woken and the next tick reads the new project

#### Scenario: A project is edited, paused or deleted

- **WHEN** the edit, pause or delete route completes its writes
- **THEN** the loop is woken exactly as after a create

#### Scenario: A verdict is posted on the board

- **WHEN** the verdict route has posted the accept or reject note on the change request
- **THEN** the loop is woken, so the tick captures the verdict and the merge does not wait for the next poll

#### Scenario: The write is refused

- **WHEN** any of those routes refuses the request (409 stale read, 422 validation, 500)
- **THEN** the loop is not woken, because the state the tick would read did not change

### Requirement: A wake never blocks the request and never needs a loop

The wake SHALL be a non-blocking signal, SHALL coalesce while one is pending, and SHALL be safe when no loop is
running in the process.

#### Scenario: One-shot command with no loop

- **WHEN** the same routes are built by a command that runs no mirror loop
- **THEN** the response is unchanged and no goroutine or channel is left waiting
