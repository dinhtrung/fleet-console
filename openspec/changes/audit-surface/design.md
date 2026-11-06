# Design — reading the ledger

## D1. Keyset on `id DESC`, never `OFFSET`

`event` is append-only and its `id` is the insertion order, so `WHERE id < ?before ORDER BY id DESC LIMIT n` is
stable under concurrent writes and costs one index seek. `OFFSET` would both drift (rows inserted between pages)
and scan. The response carries `next_before` = the smallest id in the page, and the last page returns an empty
`next_before` rather than a total count: counting a ledger on every request is the expensive half of paging and no
screen needs it.

Rejected: `page`/`pageSize` like the mock. It cannot answer "what happened after this row" without a second query,
and it is the one shape that breaks when the writer is busy — which is exactly when the operator is reading.

## D2. Filters are a closed set, and an unknown value is a 400

`actor`, `kind`, `subject`, `since`, `until` are the only parameters. Each is validated (a known actor shape, a
known kind prefix, an RFC3339 timestamp) and anything else is `400 unsupported filter`, because a filter the
server silently ignores tells the operator "nothing matched" when the truth is "you asked a question I did not
understand". `limit` keeps its existing 1..500 clamp.

`subject` is a prefix match (`#157`, `fleet-console`, `fleet-console#157`) so one parameter answers both "this
item" and "this project". `since` and `until` compare **instants**, not text: `at` is written RFC3339Nano, whose
text order disagrees with instant order whenever the fractional seconds differ in length — as strings
`"…10:00:01Z"` sorts *after* `"…10:00:01.5Z"`, so a text `since` would keep a row an instant bound excludes. The
store compares with `julianday(at)`, which reads both spellings as the instants they are.

## D3. Detail is one row, not a second query language

`GET /api/events/:id` returns the row plus its `exit_code` and `stdout_hash`. It does not re-read the command's
output: the hash is what the audit contract records, and pretending to fetch the output would be a new storage
promise. The screen renders the same fields as the list, uncut.

## D4. An empty log and an unreadable log are different states

The mock's own comment says it: "an empty audit list must not be confused with a board that has no events at all".
The real screen keeps three states — loading, empty (`no events match`), and error (`the log could not be read:
<err>`) — and the error state is a retry, not an empty table. That matters because `/api/events` failing is the
symptom of a broken store, which is the one failure the operator must not read as "quiet".

## D5. The mock functions are deleted, not deprecated

`listAudit`, `getAuditEvent` and `auditActions` leave `src/mock/api.ts`. A screen that keeps a mock fallback behind
an error is worse than one that shows the error: the fallback is what makes a dead endpoint look alive.
