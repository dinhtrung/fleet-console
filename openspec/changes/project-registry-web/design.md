# Design — project registry written from the board

## 1. The file is the source of truth; the DB is a cache of it

`--apply-config` already runs the arrow file → DB, and the act loop reads the DB. The web path is the same arrow,
run eagerly: write the file, then upsert, then answer. Nothing is written to the DB that the file does not name.
Removal stays the one-directional problem it already is (`--prune`); this change only ever adds.

## 2. Writing YAML without destroying the file

The file carries a header comment, per-project spacing and blank lines. Constraints that follow:

| Approach | Verdict |
|---|---|
| Decode into `config.Config`, re-encode | Rejected: the struct has no comment storage, the file comes back flattened and unreadable |
| Keep projects in a second include file | Rejected: `--config` names one file; a second file breaks the single-file story and the installer |
| Template text around a marker block | Rejected: the projects sequence is not a marker block, and a partial render is the failure mode we are avoiding |
| `gopkg.in/yaml.v3` node tree, upsert one node, re-encode | **Chosen**: yaml.v3 keeps `HeadComment`/`LineComment`/`FootComment` and key order on the nodes it decodes |

The document node is decoded once per write; the `projects:` key is located in the root mapping node; the new entry
is appended as a `yaml.Node` built from the same struct tags the loader reads. A missing `projects:` key is created
(an empty file is a valid config). An existing key is refused.

## 3. One writer, atomic

`internal/configfile` owns every write:

- `UpsertProject(path string, in config.Project) (Outcome, error)` — `Outcome` is `Created` or `Unchanged`.
- **Atomicity:** encode into a temp file in the same directory, `fsync`, `chmod` to the mode the original had,
  `rename` over the target. A crash leaves either the old file or the new one, never half of one.
- **Single writer:** one process mutex, because the service is the only writer. Two `fleet-console` processes on one
  config file is already a configuration error.
- **Stale read:** the current file's sha256 is compared with the one the request read. A mismatch answers
  `409 config.yaml changed on disk; reload and retry` instead of clobbering a hand edit.

## 4. Validation: one rule set, two callers

The handler does not invent its own rules. `config.ValidateProject(p Project) error` is exported from the package
the loader uses, and the loader calls it while decoding, so a project the board accepts is a project the next boot
accepts. Rules: `key` matches `^[a-z0-9][a-z0-9-]{0,31}$`, `name`/`repo` non-empty, `forge` in `github|gitlab`,
`default_branch` non-empty, `rigs` non-empty, `concurrency_cap >= 0`.

Paths are expanded (`~`, relative to the config file's directory) before anything looks at them. A missing
`paths.main` is **accepted** and returned as a warning, not an error: cloning the repository is the operator's next
step, and refusing the entry would force them to create the project in the wrong order.

## 5. HTTP contract

- `GET /api/projects` → `200 {projects: [{key, name, forge, repo, default_branch, rigs, concurrency_cap,
  paths, source: "file"}]}`
- `POST /api/projects` → body is the project shape the file uses (camel or snake case is not accepted; the tags are
  the file's: `key, name, forge, repo, default_branch, rigs, concurrency_cap, paths.main, paths.worktrees`).
  - `201` the stored project, plus `warnings: []`
  - `400` malformed JSON
  - `422 {errors: {<field>: <message>}}` when validation fails; nothing written
  - `409 {error: "..."}` for a duplicate key or a config file that changed on disk
- Both routes are inside the existing `bearerAuth` group, so a loopback page session (or the operator token) is the
  only way in.

## 6. Board surface

- Route `/projects`, nav entry in the Board group next to Board/Decisions/Seats.
- Table: key, name, forge badge, repo, rigs, cap. Empty state explains that the file is empty.
- New project dialog (the kit's `Dialog`): key, name, forge (select), repo, default branch, rigs (comma list),
  concurrency cap, `paths.main`, `paths.worktrees`. Client-side rules mirror the server's field rules so the common
  mistake costs no round trip; the server's `422` renders per field anyway.
- On `201`: dialog closes, a toast names the project, `["projects"]` and `["board"]` are invalidated so the project
  appears immediately. Warnings render as a non-blocking note inside the dialog after success ("paths.main does not
  exist yet: clone it before the first dispatch").

## 7. Tests that prove the promises

| Promise | Test |
|---|---|
| Comments and other projects survive | fixture file with header comment + 2 projects; upsert; compare text |
| Idempotent | upsert the same entry twice → `Unchanged`, file byte-identical |
| Atomic | a write that fails mid-encode leaves the original bytes (temp file asserted removed) |
| Stale read refused | touch the file between read and write → `409` |
| One rule set | table test calling the handler and the loader with the same bad project, same message |
| Mirrors without restart | handler test: POST → row in DB → project listed by the store the mirror reads |
