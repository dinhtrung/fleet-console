# board-navigation Specification

## Purpose
The board's shipped screens read the real API, and the rail lists every surface that exists.

## Requirements

### Requirement: Every shipped screen reads the real API

No screen under `src/screens` SHALL import from `src/mock`. The router SHALL register only screens that read
`src/lib/fleet.ts`. A screen that requires backend data the app does not have SHALL be deleted rather than shipped
against fixtures.

#### Scenario: A mock import creeps back in

- **WHEN** a change adds `import … from "../mock/api"` to a file under `src/screens`
- **THEN** `npm run check:screens` fails and names the file, and CI fails with it

#### Scenario: The deleted routes

- **WHEN** the operator opens a URL whose screen was removed, such as `/users`
- **THEN** the router answers its not-found surface, with no mock data and no redirect to the board

### Requirement: The rail lists every real surface

The navigation SHALL list Board, Decisions, Seats, Projects, Culture, Audit and Settings, each gated by the
permission the app declares for it. It SHALL NOT list a surface the app cannot serve.

#### Scenario: Audit and settings are reachable

- **WHEN** the board loads with the operator's permissions
- **THEN** the rail shows Audit and Settings beside the existing five, and each opens a real screen

#### Scenario: A permission the module does not declare

- **WHEN** a navigation item carries a permission that `src/access/access.tsx` does not declare
- **THEN** the item is hidden, never shown by default

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
