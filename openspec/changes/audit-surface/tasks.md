# Tasks — the audit surface

## 0. Ground rules

- Gate: `go build ./... && go vet ./... && go test ./...` and, for the screen task, `npm run build` plus
  `npm run typecheck`. `openspec validate --all --strict` green. Paste the real output.
- Fixed vocabulary: parameters `actor`, `kind`, `subject`, `since`, `until`, `limit`, `before`; response keys
  `events`, `next_before`; error `unsupported filter: <name>`.
- No schema migration. `event` already has every column this needs.
- Never invent a total count, an export or a full-text search.

## 1. Store and API (builder)

- [x] 1.1 `internal/store/queries.go`: `EventFilter{Actor, Kind, SubjectPrefix, Since, Until string; Before int64;
      Limit int}` and `Events(ctx, filter)` with keyset `id DESC`; keep `RecentEvents` as a thin wrapper so
      existing callers and tests stay green.
      Evidence: store test with 5 rows, page size 2 — pages `[5,4]`, `[3,2]`, `[1]`, cursor empty at the end, and a
      filter by actor and by subject prefix.
- [x] 1.2 `internal/web/fleet.go`: parse and validate the parameters; unknown parameter → `400 unsupported filter`;
      unparseable time → `400`; `limit` clamped 1..500 as today. `GET /api/events/:id` → 404 when absent.
      Evidence: handler tests for the three 400s, the 404, and a happy page whose `next_before` is asserted.
- [x] 1.3 The 400s are sent without touching the store: assert the fake store recorded zero calls.

## 2. Screen (builder, blocked by 1)

- [x] 2.1 `src/lib/fleet.ts`: typed `AuditEvent`, `AuditPage`, `listEvents(filter)`, `getEvent(id)`.
- [x] 2.2 `AuditListScreen.tsx` / `AuditDetailScreen.tsx`: real reads, filter bar bound to the URL (`src/lib/urlState.ts`
      exists for this), keyset pager, and the three states from the spec. Remove the mock imports.
- [x] 2.3 `src/mock/api.ts`: delete `listAudit`, `getAuditEvent`, `auditActions` and any seed type only they used;
      `rg -n 'mock' src/screens/Audit'` returns nothing.
- [ ] 2.4 Evidence: `npm run build` output and a screenshot of `/audit` against a dev server with
      `act: false` and a throwaway database — never port 8787.
