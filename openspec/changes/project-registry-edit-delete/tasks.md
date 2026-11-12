# Tasks — a project is edited and deleted from the board

A box is ticked only with the command that proved it, quoted in the pull request.

## 0. Ground rules

Fixed here so two builders cannot invent two names. Anything this section does not fix is a decision the issue
must ask for, not guess.

**Routes** (all inside the existing `bearerAuth` group, registered in `RegisterProjectsRoutes`,
`internal/web/projects.go:92`):

| Method | Path | Body | Success |
|---|---|---|---|
| `GET` | `/api/projects` | — | `200 {projects: [...]}` merged file+store |
| `PUT` | `/api/projects/{key}` | the file's project shape | `200 {project, changed: [..], warnings: [..]}` |
| `PUT` | `/api/projects/{key}/onhold` | `{"onhold": true}` | `200 {project}` |
| `DELETE` | `/api/projects/{key}?confirm={key}` | — | `200 {deleted: {items, dispatches, verdicts, snapshots, repos}}` |

**Field classification** (one table, used by the handler, the tests and the UI):

| Class | Fields | Where it lives |
|---|---|---|
| file-owned, harmless | `name`, `concurrency_cap`, `gate_cmd`, `labels`, `runtime`, `culture_file` | file, copied to the store by `registry.ApplyOne` |
| file-owned, identity (frozen while a dispatch is live) | `forge`, `repo`, `repo_url`, `default_branch`, `paths.main`, `paths.worktrees`, `rigs` | same, but refused under a live dispatch |
| immutable | `key` | — (rename = delete + create) |
| DB-owned | `onhold` | store only (`SetProjectOnHold`); **never** `enabled` (design D5) |

**Errors:** `configfile.ErrNotFound` (new, `404`), `configfile.ErrStaleRead` (`409`), `configfile.ErrDuplicateKey`
(stays a create-only refusal), `*config.ValidationError` (`422`). Classify by identity, never by matching text.

**Log lines** (one per write, on the journal, same prefix as the rest of the app):

- `projects: edit key=<key> changed=<comma list>`
- `projects: onhold key=<key> onhold=<true|false>`
- `projects: delete key=<key> items=<n> dispatches=<n> verdicts=<n>`

**Audit:** one `event` row per write, actor `board`, kinds `project.edit` / `project.onhold` / `project.delete`,
subject `project:<key>`.

**Build gate** (all four, quoted in the PR):

```
go build ./... && go vet ./... && go test ./...
npm run typecheck && npm run lint && npm run build
openspec validate --all --strict
make build          # embedded board must agree with src/
```

Do not add a CLI verb: `--apply-config` and `--prune` stay the reconciliation paths and their behaviour does not
change in this change set.

## 1. Backend — the file writer (one issue: `internal/configfile`)

- [ ] 1.1 `RemoveProject(path, key, expectedHash) (Outcome, error)` over the same `yaml.v3` node tree as
      `splice`: locate the sequence item whose `key` matches, compute its byte span from the item's start line and
      the line after the previous item's end, and splice it out. A missing key returns `ErrNotFound`; a stale hash
      returns `ErrStaleRead`; an unknown `key` never writes. Verify: `go test ./internal/configfile`, fixture with
      a header comment and three entries, removal of the middle one, text comparison.
- [ ] 1.2 Removing the **last** entry leaves a file `config.Load` accepts (`projects: []` or an empty sequence),
      not a dangling `projects:` key that fails the whole-document validation. Verify: table test — remove each of
      1, 2 and 3 entries in turn, `config.Load` after each, `len(Projects)` asserted.
- [ ] 1.3 `UpdateProject(path, in, expectedHash) (Outcome, error)` replaces the matching entry in place: same span
      as 1.1, new text from `encodeEntry` + `indentEntry` at the entry's own indentation, so the other entries'
      bytes never move. An unknown key returns `ErrNotFound`; an identical entry returns `OutcomeUnchanged` without
      writing. Verify: test asserts the other entries and the header comment are byte-identical, and that a second
      identical update is `Unchanged`.
- [ ] 1.4 Both writes run under the existing package mutex, validate the **whole merged document** with
      `config.ValidateContent` before writing, and use `writeAtomic`. Verify: test forces a validation failure
      (a duplicate key introduced by the update) and asserts the original bytes survive and the temp file is gone.
- [ ] 1.5 A removal that would leave the file unloadable is a `*config.ValidationError`, not a silent write.
      Verify: fixture where the remaining entry has a bad `forge`; removal is refused and nothing is written.

## 2. Backend — the store and the mirror (one issue: `internal/store`)

- [ ] 2.1 `SetProjectOnHold(ctx, projectID, bool) (bool, error)` mirroring `SetProjectEnabled`
      (`internal/store/registry.go:14`). Do **not** reuse `enabled`; `UpsertProject` resets it to `1` on every
      update (`internal/store/upsert.go:94`). Verify: test — set onhold, run `registry.Apply(..., syncOnHold=false)`,
      still onhold; run it with `syncOnHold=true` and no file key, still onhold.
- [ ] 2.2 `ProjectCounts(ctx) (map[int64]ProjectCount, error)` — one query returning items, dispatches, verdicts,
      snapshots and the live dispatch id per `project_id` (grouped, no per-project loop). Verify: `go test
      ./internal/store` with two projects and known counts.
- [ ] 2.3 `ReplaceItems` must not insert for a project row that no longer exists: turn the insert into
      `INSERT … SELECT … WHERE EXISTS (SELECT 1 FROM project WHERE id = ?)` (`internal/store/mirror.go:36`).
      Verify: test deletes the project row, calls `ReplaceItems`, asserts zero `item` rows exist for that id and
      the call does not error.
- [ ] 2.4 Deleting a project's mirrored items after a repo/forge change: a small store helper
      (`ClearItems(ctx, projectID) error`) reusing the same statement, so the handler does not open its own
      transaction. Verify: test — seed items, clear, zero rows.

## 3. Backend — the routes (one issue: `internal/web/projects.go`)

- [ ] 3.1 `GET /api/projects` merged view: `config.Load` ∪ `store.Projects`, joined on key, with `source`
      (`file`/`both`/`db`), `enabled`, `onhold`, `items`, `dispatches`, `live_dispatch`. No forge call anywhere on
      this path. Verify: `httptest` table with the three `source` fixtures and counts; a fake forge whose methods
      fail the test if invoked.
- [ ] 3.2 `PUT /api/projects/{key}`: body key must equal the path key (`422` otherwise), `config.ValidateProject`
      on the merged entry, `UpdateProject`, reload, `registry.ApplyOne`, `event` row, response with `changed`.
      `ErrNotFound` → `404`; `ErrStaleRead` → `409`; `422` field map as the create route already answers. Verify:
      table per status in `internal/web/projects_test.go`.
- [ ] 3.3 Live-dispatch rule: read `store.LiveDispatch` before the write; refuse (`409`) when an identity field
      changed, naming dispatch id, item and branch; let harmless fields through. Verify: two handler tests against
      a temp store with a live dispatch row (identity refused, cap accepted) and the file's mtime unchanged in the
      refused case.
- [ ] 3.4 Repo/forge change clears items in the same request, after the store upsert, before the response.
      Verify: handler test seeds items, changes `repo`, asserts `GET /api/board` shows zero items for the project.
- [ ] 3.5 `PUT /api/projects/{key}/onhold`: `SetProjectOnHold` + `event` row, never a file write. Verify: handler
      test asserts the DB flag flipped and the config file's bytes are identical.
- [ ] 3.6 `DELETE /api/projects/{key}`: `confirm` must equal the key (`400` otherwise) → `LiveDispatch` check
      (`409`) → `onhold` → `RemoveProject` (a `db`-source project skips this step) → `store.DeleteProject` →
      `event` row → counts in the body. A failure after the pause answers `500` naming the step. Verify: handler
      tests for each status, the pause-then-fail case, and a replay that completes.
- [ ] 3.7 `WriteProject` stays the injection point for tests, but the delete needs its own hook
      (`RemoveProject func(...)`) in `web.Config`, defaulting to `configfile.RemoveProject`, so the failure test
      can refuse the file step without touching permissions. Verify: `internal/web/app.go` wires it and the test
      uses it.
- [ ] 3.8 One `event` row per write, written after the store change and before the response, actor `board`.
      Verify: handler test reads `/api/events` and asserts kind, subject and the `old → new` detail.

## 4. Web — the Projects surface (one issue: `src/`)

- [ ] 4.1 `src/lib/fleet.ts`: `updateProject(key, input)`, `setProjectOnhold(key, onhold)`, `deleteProject(key,
      confirm)` typed against the design's shapes, reusing `request` (which already sends `Idempotency-Key`);
      extend `Project` with `source`, `enabled`, `onhold`, `items`, `dispatches`, `live_dispatch`. Verify:
      `npm run typecheck`.
- [ ] 4.2 `ProjectsScreen.tsx` row actions: an actions cell with Edit / Pause·Resume / Delete, disabled on the
      state that makes them invalid (`Edit` on a `db`-source row, with a title explaining the file is the source).
      Verify: `npm run build`, then the built binary on a spare port (`scripts/spare-port-route-probe.sh`) with
      `act: false`.
- [ ] 4.3 Edit dialog reuses the create form: `key` rendered read-only with the immutable-key hint, fields
      prefilled from the list row, `422` mapped onto fields, `409` from the live-dispatch rule rendered as the
      dialog-level message **with the remedy** ("pause, wait for the row to end, edit again"). Verify: create a
      project, edit its cap, confirm the table and the file both changed.
- [ ] 4.4 Delete dialog: shows `items`, `dispatches` and the live dispatch if present, lists the rigs that will
      keep running, requires the key to be typed before the confirm button enables, and on `409` tells the
      operator to pause first. Verify: dialog stays disabled until the key matches; a delete removes the row and
      invalidates `["projects"]` and `["board"]`.
- [ ] 4.5 Rebuild the embedded board (`npm run build`, copy `dist/` to `internal/webui/dist/`) in the same commit
      as the `src/` change. Verify: `make build` and `GET /` still 200 from the built binary.

## 5. Docs (one issue, docs-only PR allowed)

- [ ] 5.1 `README.md`: the registry section says the board edits, pauses and deletes, and that delete is the same
      cascade as `--prune`; the command table keeps its rows unchanged.
- [ ] 5.2 `docs/quickstart.md` step 5: one sentence pointing at the row actions, and the pause-is-DB-owned rule.
- [ ] 5.3 `docs/fleet-console.md`: the `onhold` paragraph gains the board route and the `--sync-onhold` override.

## 6. Gate

- [ ] 6.1 `go build ./... && go vet ./... && go test ./...` — full output in the PR.
- [ ] 6.2 `npm run typecheck && npm run lint && npm run build` — full output in the PR.
- [ ] 6.3 `openspec validate project-registry-edit-delete --type change --strict` and
      `openspec validate --all --strict`, with the totals line quoted.
- [ ] 6.4 `make build`, then the spare-port probe proves the four routes answer their documented statuses against
      a throwaway config, database and `act: false` — never on 8787.
