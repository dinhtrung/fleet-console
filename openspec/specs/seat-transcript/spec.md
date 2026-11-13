# seat-transcript Specification

## Purpose
The task drawer shows the dispatch seat's own pane on demand, and an item with no dispatch seat shows none.

## Requirements

### Requirement: The task drawer shows the dispatch seat's pane on demand

For an item with a dispatch seat, the drawer SHALL offer a transcript panel that reads
`GET /api/seats/:session/tail` for that seat when the operator opens it, rendering the returned text without
modification. It SHALL NOT poll.

#### Scenario: Opening the transcript

- **WHEN** the operator expands the transcript on an item whose dispatch names `dev-build-1@fleet-console`
- **THEN** one request is made for 40 lines of that session and the pane text is shown as preformatted text

#### Scenario: Asking for more

- **WHEN** the operator asks for more lines
- **THEN** the request asks for 200, the route's own ceiling, and no more

#### Scenario: The seat cannot be read

- **WHEN** the route answers 502
- **THEN** the panel shows the error with a retry, not an empty pane

### Requirement: An item without a dispatch seat shows no transcript

The drawer SHALL render no transcript panel when the item has no dispatch row, and SHALL say the item was never
handed to a seat rather than naming a session derived from the branch or the project's rig list.

#### Scenario: A hand-made item

- **WHEN** an item has no dispatch row
- **THEN** the drawer shows its existing `No dispatch` state and no transcript panel or tail request
