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

### Requirement: A paused project's header says paused

The project header SHALL render a paused project's forge read as `paused — no forge reads` in state `paused`,
whatever the stamp's age, instead of the age-based quiet or stalled state.

#### Scenario: A day-long pause

- **WHEN** a project has been paused for a day and its forge stamp is a day old
- **THEN** the header says paused, not stalled

#### Scenario: Resume resets the clock

- **WHEN** the project is resumed and the next tick reads the forge
- **THEN** the header shows the fresh age and its normal state again

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

### Requirement: The shell renders no demo scaffolding

The console SHALL NOT render a scope switcher, demo identity or account block, permission-filtered navigation,
failure/latency/reset control, synthetic-data notice, demo footer or demo attribution copy.

#### Scenario: The rail

- **WHEN** the board loads
- **THEN** the rail shows navigation and real board controls only — no Scope dropdown, no database menu, no account
  block

#### Scenario: The footer

- **WHEN** any screen is open
- **THEN** no line says the board is synthetic, demo, authored or not a real service

### Requirement: The console ships no mock data

Every shipped module SHALL read data from `src/lib/fleet.ts` or a local display preference. `src/mock/**` and
`public/mock/**` SHALL NOT exist, and nothing SHALL import a path reaching `/mock`.

#### Scenario: A clean build

- **WHEN** `npm run build` runs
- **THEN** no module resolves a path under `src/mock`, and the built board reads the real API

#### Scenario: The tree

- **WHEN** the source tree is listed
- **THEN** neither `src/mock` nor `public/mock` is present

### Requirement: No identity or permission fiction

The console SHALL NOT render a user name, email or role-derived permission gate. The rail SHALL list every shipped
surface; the server's token SHALL remain the only authorization.

#### Scenario: Every surface is reachable

- **WHEN** the board loads
- **THEN** every shipped route appears in the rail and no account block is drawn

#### Scenario: The token is refused

- **WHEN** a request reaches the server without a valid token
- **THEN** the server answers its auth error and the screen shows its error state; the client did not pre-hide the
  surface

### Requirement: A guard refuses demo scaffolding

A repository check SHALL fail when a demo token or a mock import appears in `src/`, `index.html` or `public/`, and
the web CI job SHALL run it. The check SHALL name every offending file and line.

#### Scenario: Scaffolding returns

- **WHEN** a change adds `import … from "../mock/api"` or the string `Synthetic data`
- **THEN** the check exits non-zero naming that file and line

#### Scenario: A clean tree

- **WHEN** the tree carries no demo token and no mock import
- **THEN** the check exits 0 and the web job passes
