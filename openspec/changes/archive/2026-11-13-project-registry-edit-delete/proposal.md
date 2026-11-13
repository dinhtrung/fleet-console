# Proposal — a project is edited and deleted from the board

## Why

The registry has three write paths today and the board owns one direction of one of them: `POST /api/projects`
creates (`internal/web/projects.go:94`), while every other change is `ssh`, edit
`~/.config/fleet-console/config.yaml`, run `--apply-config`, and — to retire anything — run `--prune`, which
refuses while a dispatch is live (`internal/registry/registry.go:101`). Three consequences:

- A wrong `concurrency_cap`, a repo that moved, a rig that should be second in the tier list: nothing on the
  board can fix it, and a hand edit does not reach the DB until the next `--apply-config` or a restart. The
  operator's only feedback is the journal.
- A project can be retired only by a command that also deletes its history — `store.DeleteProject` removes the
  project's `item`, `dispatch`, `verdict`, `snapshot`, `mirror_meta` and `repo` rows
  (`internal/store/registry.go:40`) — and nothing shows how much history that is before it happens.
- The board lists `config.Load` only (`internal/web/projects.go:101`), so a project that exists in the DB and
  not in the file — exactly the drift `--prune` exists to clean — is invisible at the moment it matters.

## What changes

- `PUT /api/projects/{key}` — edits the file-owned definition (name, forge, repo, branch, rigs, runtime, cap,
  gate, labels, culture file, paths). File first, then the store row: the same arrow `--apply-config` runs. The
  answer names the fields that changed and carries the loader's warnings.
- `DELETE /api/projects/{key}?confirm={key}` — removes the entry from `config.yaml` and deletes the project's
  app-owned rows through the store call `--prune` already uses (`store.DeleteProject`). Refused while a
  dispatch is live, naming it.
- `PUT /api/projects/{key}/onhold` — the reversible stop. Writes the DB-owned `onhold`, never the file.
- `GET /api/projects` becomes the merged registry: file entries plus DB rows, each with `source`, `enabled`,
  `onhold`, item and dispatch counts and the live dispatch id, so a delete can show what it costs before it runs.
- The Projects surface grows row actions — Edit, Pause/Resume, Delete — a prefilled edit dialog, and a delete
  dialog that requires the key to be typed.

## Losses, named

- **A board delete is exactly as destructive as `--prune`**: the project's items, dispatches, verdicts and
  snapshots are gone; `event` rows survive as the audit trail. The board removes the ssh step, not the loss.
- **Key rename is not offered.** A key is the identity that rows, labels and queue bodies carry; renaming is a
  delete plus a create, and the route says so instead of half-doing it.
- **A delete does not stop a running rig.** Its seats keep burning quota until `rig down <rig>`; the dialog
  names the rigs, and the app does not stop somebody's seats as a side effect of a registry edit.
- **Identity fields are frozen while a dispatch is live** (forge, repo, default branch, paths, rigs). The
  operator's path is Pause → wait for the row to end → edit. Refusing beats moving a worktree out from under a
  running builder, which no code path repairs.
- **Pause lives in the DB only.** A file that pins `onhold: true` still wins on an explicit
  `--apply-config --sync-onhold`; the two layers can disagree until the file is edited, and the UI names the
  layer it wrote.

## Capabilities

### New Capabilities

- `project-registry` — one more write direction over the registry: edit, pause and delete from the board, with
  the config file as the source of truth and the store as its cache.

## Impact

- `internal/configfile`: `UpdateProject` and `RemoveProject` over the same `yaml.v3` node tree and the same
  mutex, plus an `ErrNotFound`; the span removed comes from the sequence nodes, not from re-rendering.
- `internal/store`: `SetProjectOnHold` (the `enabled` flag is the wrong lever — see design D5), a counts query
  for the merged list, and `ReplaceItems` must not insert rows for a project row that no longer exists
  (`internal/store/mirror.go:26`).
- `internal/web/projects.go`: three new handlers, the merged list, one `event` row per write.
- `src/screens/ProjectsScreen.tsx`, `src/lib/fleet.ts`, the embedded board in `internal/webui/dist`.
- Docs: `README.md` (registry section), `docs/quickstart.md` step 5, `docs/fleet-console.md`.

## Out of scope

- Bulk operations, reordering projects, and the file's non-project keys (the settings surface owns those).
- Deleting a project's git worktrees or branches; housekeeping already reclaims an unmerged worktree.
- Stopping a rig. `rig_lifecycle` is the only path that stops seats, and it is a per-project opt-in.
