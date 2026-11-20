## ADDED Requirements

### Requirement: The audit surface reads the app's own event log

`GET /api/events` SHALL read the `event` table and SHALL page by keyset on `id DESC`, returning the page's rows and
the `next_before` cursor. `GET /api/events/:id` SHALL return one event. The screens at `/audit` and `/audit/:id`
SHALL render only these two reads.

#### Scenario: The newest page

- **WHEN** the operator opens `/audit` with no filter
- **THEN** the screen shows the newest events, newest first, with a control that asks for the next page by
  `next_before`

#### Scenario: The last page

- **WHEN** a page returns fewer rows than the limit
- **THEN** `next_before` is empty and the screen offers no further page

#### Scenario: One event

- **WHEN** the operator opens `/audit/<id>`
- **THEN** the detail shows that event's actor, kind, subject, detail, timestamp and, when present, its exit code
  and stdout hash

### Requirement: Filters are applied by the server and are a closed set

The read SHALL accept `actor`, `kind`, `subject`, `since` and `until`, all applied in the query. A parameter outside
that set, or a value that does not parse, SHALL answer 400 naming it. The server SHALL NOT ignore a filter it does
not understand.

#### Scenario: One project's events

- **WHEN** the operator filters by `subject=fleet-console`
- **THEN** only events whose subject starts with that key are returned, and the cursor stays consistent

#### Scenario: A filter the server does not know

- **WHEN** the request carries `severity=high`
- **THEN** the response is 400 naming `severity`, and no rows are returned

#### Scenario: A malformed timestamp

- **WHEN** `since` is not an RFC3339 instant
- **THEN** the response is 400 naming `since`, rather than silently returning everything

### Requirement: An empty log is distinct from an unreadable one

The screen SHALL render an empty result as `no events match` and a failed read as an error with a retry. A failed
read SHALL NOT be rendered as an empty list.

#### Scenario: The store cannot be read

- **WHEN** `/api/events` answers 500
- **THEN** the screen shows the error with a retry, and never an empty table

#### Scenario: Nothing matches

- **WHEN** a valid filter matches no row
- **THEN** the screen says so and keeps the filter visible
