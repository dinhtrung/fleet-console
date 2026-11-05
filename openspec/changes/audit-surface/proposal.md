# Proposal — an audit surface that reads the app's own log

## Why

Every write this app performs already lands in `event`: `act-sync`, `dispatch`, `verdict`, `merge`,
`tick.blocked`, `reconcile`, and the new lifecycle rows. The board exposes `GET /api/events`
(`internal/web/fleet.go:682`) and nothing else: it returns the newest 50 rows, has no filter, no paging and no
detail route.

Meanwhile `/audit` and `/audit/$eventId` are the imported admin-panel **mock**
(`src/screens/AuditListScreen.tsx:17` imports `../mock/api`). So the one screen whose job is to answer "who did
what, when, and why did it fail" shows invented data, while the real answer sits one route away — filtered by
nothing an operator can use.

The cost is concrete: the merge gate's decisions are only explainable from the event stream (a `merge` row names
the sha; a `tick.blocked` row names the gate refusal). Today reading them means `journalctl` and `sqlite3` on the
box.

## What changes

- `store.Events(ctx, filter)` — keyset paging on `id DESC`, with server-side filters: `actor`, `kind`, `subject`
  prefix (a project key or an issue number), `since`, `until`, `limit`.
- `GET /api/events` gains those query parameters and returns `{events, next_before}`; `GET /api/events/:id` returns
  one event. Unknown parameter values are a 400, never a silently ignored filter.
- `/audit` renders the real list with the existing table shell: filter bar, keyset pager, and the event detail
  panel; the mock audit functions are deleted.

## Losses, named

- No full-text search over `detail`. SQLite FTS would be a second index to keep true; the filters cover the
  questions the ledger is actually read for.
- No export. A copy-paste of the detail is the escape hatch until someone needs a file.

## Capabilities

### New Capabilities

- `audit-log`: the read surface over `event` — filters, keyset paging, one-event detail, and the distinction
  between an empty log and an unreadable one.

## Impact

- `internal/store/queries.go` — `Events` with a filter; `RecentEvents` stays as the internal shorthand.
- `internal/web/fleet.go` — the two handlers, the parameter parsing, the 400s.
- `src/lib/fleet.ts` — the typed reads; `src/screens/AuditListScreen.tsx`, `AuditDetailScreen.tsx` — real data.
- `src/mock/api.ts` — the audit functions go; nothing else in the tree reads them.
- No schema change: `event` already carries actor, kind, subject, detail, exit code and stdout hash.
