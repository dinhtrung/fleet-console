# Design — a project is edited and deleted from the board

## D1. Delete runs pause → file → rows, because that is the only order whose half-finished state is safe

`store.DeleteProject` is one transaction over seven tables (`internal/store/registry.go:40`), but the file write is
not in it, so some step can land alone. The orders and what a failure leaves behind:

| Order | Failure leaves |
|---|---|
| rows → file | the file still names the project; the next boot or `--apply-config` upserts it back as an empty project (`registry.ApplyOne` sets `Enabled: true`) — a resurrection nobody watched |
| file → rows | the entry is gone but the row is still **enabled**, so the act loop keeps planning and dispatching a project the operator thinks is deleted |
| pause → file → rows | the project is `onhold`: the act loop skips it (`internal/decide/decide.go:205`), the board still shows it, and a retry completes the deletion |

**Chosen:** refuse on a live dispatch, then `onhold = true`, then remove the file entry, then delete the rows. A
failure answers `500` naming the step, and the retry is the same request — every step is idempotent, so the second
attempt resumes instead of starting over.

`Rejected:` rows-first, because its failure mode is a silently resurrected and enabled project.

## D2. Edit is a PUT of the whole definition, not a patch

The dialog already renders every field on the create path (`src/screens/ProjectsScreen.tsx`), so the edit form is
the same form prefilled. A full PUT means one validator (`config.ValidateProject`), one error map, and no
ambiguity about "clear this field" — a partial PATCH cannot distinguish an omitted key from an empty one without a
third state on every field.

The handler diffs the stored entry against the submitted one to produce `changed: [...]`, which is what the audit
row and the response carry. `key` is read-only in the body: a PUT whose body key differs from the path key is `422`
on `key` (D8).

`Rejected:` PATCH with the file's field names — a second validator, a second error map, and no way to express an
intentional empty.

## D3. Identity fields are frozen while a dispatch is live; the rest apply

The builder's worktree is cut from `paths.worktrees` and its branch is pushed to `repo`; moving either while the
row has no end time loses that work at merge time, and nothing in the app rebases or re-dispatches it.

- **Identity:** `forge`, `repo`, `repo_url`, `default_branch`, `paths.main`, `paths.worktrees`, `rigs`.
- **Harmless:** `name`, `concurrency_cap`, `gate_cmd`, `labels`, `runtime`, `culture_file` — each is read per tick
  or at the next dispatch, so a change lands without touching work in flight.
- An identity change while `store.LiveDispatch(projectID)` reports a row answers `409` naming the dispatch id, the
  item and the branch. The refusal is the instruction: pause, let the row end, then edit.

`Rejected:` refusing every edit while live — the cap and the labels are exactly what an operator changes mid-flight.

## D4. A repo or forge change clears the mirrored items, and a write for a deleted project inserts nothing

Items are mirrored per project and replaced on every tick (`internal/store/mirror.go:26`), but "the next tick
replaces the set" is not true for the window in between: an item from the old repository would still be planned
and could be dispatched into a branch of the new one. So a successful edit that changes `forge` or `repo` deletes
that project's `item` rows in the same request, before the response. Their dispatches end at the next plan pass by
the existing rule (`internal/decide/plan.go:143`, `dispatch.prune`).

The same file holds the other half: `ReplaceItems` deletes and re-inserts inside a transaction opened after the
tick read the project row. A delete that lands mid-tick then leaves `item` rows for a `project_id` that no longer
exists. Those orphan rows are invisible (every read joins `project`), unreachable, and never cleaned, so the item
insert must be a no-op when the project row is gone: `INSERT … SELECT … WHERE EXISTS (SELECT 1 FROM project WHERE
id = ?)`.

`Rejected:` a foreign key on `item.project_id` — a migration for a race that a `WHERE EXISTS` closes in one line,
on databases that are already deployed at schema version 4.

## D5. Pause writes `onhold`, never `enabled`

`enabled` looks like the pause flag and is the wrong one: every upsert sets `enabled = 1` unconditionally
(`internal/store/upsert.go:94`), so the boot pass — which runs `registry.Apply` on every start — re-enables a
disabled project, and `--apply-config` does the same. `onhold` is the DB-owned flag that survives both, because the
store only copies it when the caller passes `SyncOnHold` (`internal/store/upsert.go:59`).

So the pause route sets `onhold` and leaves the file alone. The consequence is named in the UI: an explicit
`--apply-config --sync-onhold` copies the file's `onhold:` over the board's, and a file that pins `onhold: true`
therefore wins.

`Rejected:` `SetProjectEnabled` behind a pause button — it reads correct on the board and is undone by the next
restart, which is the kind of bug that gets reported as "the pause button does nothing".

## D6. The board's delete *is* `--prune` for one project

Same store call, same refusal (a live dispatch), same cascade. Nothing in this change invents a second deletion
semantics: `registry.Prune` deletes the rows of every project absent from the file, and the route deletes the rows
of the one project the operator named, after removing it from the file first. `event` rows are not touched, by
either path.

`Rejected:` a `deleted_at` soft delete — every read path would need the filter, the DB would grow forever, and
`enabled`/`onhold` already provide the reversible stop this protects.

## D7. One merged list, with counts, and no forge calls

`GET /api/projects` reads the file (`config.Load`) and the store (`store.Projects`) and joins on key:

| `source` | meaning | edit | delete |
|---|---|---|---|
| `file` | in the file, no row (a fresh file the boot pass has not applied yet) | yes | yes (rows already absent) |
| `both` | in both — the normal case | yes | yes |
| `db` | a row the file does not name — the `--prune` candidate | **no**, `409` naming the file | yes |

Counts (`items`, `dispatches`, `live_dispatch`) come from one grouped query over the two tables, not one query per
project, and the route makes **no forge call** — the list is rendered on every visit and a per-project `gh`/
`glab` read would put the board's own traffic into the mirror's rate-limit budget.

The counts live in the list rather than in a `/impact` route: two reads that can disagree, and the dialog's
warning must describe the same numbers the delete will act on.

`Rejected:` a separate impact endpoint, and per-project count queries in a loop.

## D8. A key is immutable

`key` is the identity that `item.project_id`, the dispatch rows, the `status::ongoing` label and every queue body
are keyed through (`store.UpsertProject` upserts on key). Renaming would have to move rows between project ids and
rewrite queue bodies the app does not own, so a rename is a delete and a create, and both routes say so:

- PUT with a body `key` different from the path → `422 {errors:{key:"key is immutable; delete and recreate"}}`.
- The delete dialog's typed confirmation is the key, which is also the guard against deleting the wrong row.

`Rejected:` a rename field — it is delete+create wearing a different name, with a data-migration cost nobody
budgeted.

## D9. One writer, stale reads, retry

`UpdateProject` and `RemoveProject` take the same package mutex and the same `expectedHash` contract as
`UpsertProject`: the bytes the request read are the bytes the write must still find, or the answer is
`409 config.yaml changed on disk; reload and retry`. A second `fleet-console` process writing the same file is
already a configuration error, so there is no cross-process protocol here.

Retry semantics differ by verb and are documented in the spec:

- `PUT` replay: the second write finds the submitted definition already on disk and answers `200` with
  `changed: []`.
- `DELETE` replay: the key is in neither layer, and the answer is `200` with `deleted: {}` — a delete that already
  happened is not an error the client should have to distinguish from a typo **if** it carries the right
  `confirm`; a wrong or missing `confirm` is still `400`.

`Rejected:` an optimistic merge of two file writers — the file is a human document, and a merge would produce a
document neither the operator nor the loader wrote.

## D10. Every registry write is one audit row

`Store.AppendEvent` exists (`internal/store/store.go:101`) and the audit surface already reads it. Each successful
write appends exactly one row: actor `board`, kind `project.edit` / `project.onhold` / `project.delete`, subject
`project:<key>`, detail naming the changed fields with `old → new`. The delete is the one that matters most: after
it, the project's own rows are gone, and that event is the only record that the deletion happened and by whom.

`Rejected:` a comment written into `config.yaml` as a changelog — the file is a configuration, not a log, and the
serializer would have to own prose.

## Status codes, in one place

| Route | 200 | 400 | 401 | 404 | 409 | 422 | 500 |
|---|---|---|---|---|---|---|---|
| `PUT /api/projects/{key}` | stored project + `changed` + `warnings` | malformed JSON | no session/token | key in neither layer | stale read; identity change under a live dispatch; store-only project | field errors; immutable key | write failed |
| `DELETE /api/projects/{key}?confirm=` | `deleted` counts | confirm missing/mismatched | same | key in neither layer | live dispatch; stale read | — | a step failed, named |
| `PUT /api/projects/{key}/onhold` | the project | malformed JSON | same | no such project | stale read (not applicable: DB only) | `onhold` not a bool | store failed |

## Tests that prove the promises

| Promise | Test |
|---|---|
| Comments and other entries survive an update and a removal | fixture with header comment + 3 projects; update one, remove another; compare text |
| The last project removed leaves a loadable file | remove the only entry → `config.Load` succeeds, `len(Projects) == 0` |
| A stale read is refused | touch the file between read and write → `409`, bytes unchanged |
| Identity change under a live dispatch | handler test with a live dispatch row → `409` naming dispatch/item/branch; file mtime unchanged |
| Harmless field under a live dispatch | same fixture, `concurrency_cap` only → `200`, row updated |
| Repo change clears items | handler test with mirrored items → `0` rows after `200` |
| Delete cascade and audit | seed items/dispatches/verdicts, delete → all gone, `event` row present, `--print-config` silent on the key |
| Failed delete leaves it paused | `WriteProject` wrapper that fails on removal → `500`, `onhold = 1`, retry → `200` |
| Pause survives a restart path | pause, run `registry.Apply(..., syncOnHold=false)` → still `onhold`; then `syncOnHold=true` with no file key → still `onhold` |
| Merge, counts and `source` | file-only, both, db-only fixtures → three `source` values, counts correct, db-only PUT `409` |
| No forge call on the list | fake forge whose methods fail the test if called, while `GET /api/projects` runs |
| Auth | no cookie and no token → `401` on all three routes, nothing written |
