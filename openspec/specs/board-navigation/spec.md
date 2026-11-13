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
