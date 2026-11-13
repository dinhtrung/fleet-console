# project-registry Specification

## Purpose
A project is created, validated and applied from the board, preserving the operator's file and refusing a duplicate or concurrent edit.

## Requirements

### Requirement: A project is created from the board
The board SHALL accept a project definition over `POST /api/projects` and SHALL persist it to the config file the
running service was started with.

#### Scenario: The operator adds a project
- **WHEN** the operator submits key `mnp-fleet`, name, forge `github`, repo `org/repo`, one rig and a concurrency cap
- **THEN** the answer is `201` with the stored project
- **AND** `fleet-console --print-config` names that project on its next run

### Requirement: The write preserves the operator's file
The writer SHALL keep the file's comments, key order and every project it did not touch, and SHALL write atomically.

#### Scenario: A file with a header comment and two projects
- **WHEN** a third project is added
- **THEN** the header comment and both existing entries are unchanged apart from the added entry
- **AND** a crash mid-write leaves either the old file or the new one, never a truncated file

### Requirement: Applying the written file is idempotent
`--apply-config` SHALL report `unchanged` for a project the board just wrote and SHALL create no duplicate.

#### Scenario: Apply after create
- **WHEN** `fleet-console --apply-config` runs on the file the board wrote
- **THEN** the project is reported `unchanged` and running it twice changes nothing

### Requirement: A duplicate key is refused
A key that already exists in the file SHALL be answered `409` and the file SHALL NOT be touched.

#### Scenario: Two projects with one key
- **WHEN** the board submits a key the file already names
- **THEN** the answer is `409` with an error naming the key, and the file's bytes are unchanged

### Requirement: Validation errors land on fields
An invalid definition SHALL be answered `422` with an error per field, and nothing SHALL be written.

#### Scenario: An uppercase key and an unknown forge
- **WHEN** the body has `key: "MNP"` and `forge: "bitbucket"`
- **THEN** the answer is `422` naming `key` and `forge`, and the file is unchanged

### Requirement: One rule set for the loader and the handler
The handler SHALL validate with the same function the config loader uses, so a project the board accepts is a project
the next boot accepts.

#### Scenario: A rule changes in one place
- **WHEN** a project is refused by the loader
- **THEN** the same project is refused by the handler with the same message

### Requirement: A concurrent edit is refused, not merged
When the config file changed on disk after the request read it, the write SHALL be refused rather than applied over
the operator's edit.

#### Scenario: Hand edit during a submit
- **WHEN** the file is modified between the board's read and its write
- **THEN** the answer is `409` naming the file, and no bytes are written

### Requirement: The new project is mirrored without a restart
A successful create SHALL upsert the project into the store in the same request, so the next mirror tick reads it
without a service restart.

#### Scenario: Create then tick
- **WHEN** a project is created and the next tick runs
- **THEN** the project appears in `/api/board` with its item counts

### Requirement: The surface sits behind the existing auth
Both project routes SHALL be served inside the authenticated API group, so a non-loopback caller without the
operator token cannot add a project.

#### Scenario: A remote caller without the token
- **WHEN** `POST /api/projects` arrives with no session cookie and no operator token
- **THEN** the answer is `401` and nothing is written

### Requirement: The Projects surface shows the registry
The board SHALL offer a Projects surface listing every project the service knows, and a form that creates one.

#### Scenario: The operator opens Projects
- **WHEN** the operator opens `/projects` with one project registered
- **THEN** the table names it with its forge, repo, rigs and cap
- **AND** the New project form creates a second one and the board shows it without a reload

### Requirement: A project is edited from the board
`PUT /api/projects/{key}` SHALL accept the file's own project shape, write the entry to the config file, then
refresh the store row on the path `--apply-config` uses, and SHALL answer the stored project, the changed field
names and the loader's warnings. Nothing SHALL be written when the definition is refused.

#### Scenario: The operator raises the concurrency cap
- **WHEN** the operator submits the project with `concurrency_cap: 3` and nothing else changed
- **THEN** the answer is `200` with `changed: ["concurrency_cap"]`
- **AND** the file and `GET /api/projects` both report `3` on the next read

#### Scenario: A refused definition writes nothing
- **WHEN** the body carries `forge: "bitbucket"`
- **THEN** the answer is `422` naming `forge`, and both the file's bytes and the store row are unchanged

### Requirement: An edit preserves the operator's file
The writer SHALL keep the file's comments, key order, spacing and every entry it did not touch, and SHALL write
atomically. Removing the last project SHALL leave a file the loader still accepts.

#### Scenario: One entry changes in a commented file
- **WHEN** a file with a header comment and three projects has its second entry edited
- **THEN** the header comment, the first and third entries and their spacing are byte-identical

#### Scenario: The last project is removed
- **WHEN** the only entry is deleted
- **THEN** `fleet-console --print-config` still loads the file and reports zero projects

### Requirement: A key cannot be renamed by an edit
A `PUT` whose body `key` differs from the path key SHALL be answered `422` naming `key`, with neither layer
written. A rename is a delete and a create, and the message SHALL say so.

#### Scenario: The body renames the project
- **WHEN** `PUT /api/projects/alpha` carries `"key": "beta"`
- **THEN** the answer is `422 {errors: {key: "key is immutable; delete and recreate"}}` and the file is unchanged

### Requirement: An edit that moves a live dispatch is refused
While the project has a dispatch with no end time, an edit changing `forge`, `repo`, `repo_url`,
`default_branch`, `paths.main`, `paths.worktrees` or `rigs` SHALL answer `409` naming the dispatch id, the item
and the branch, writing neither layer. A change to any other field SHALL apply.

#### Scenario: Moving the repo under a running builder
- **WHEN** the project has live dispatch `12` on item `#48` and the operator changes `repo`
- **THEN** the answer is `409` naming dispatch `12`, item `#48` and its branch, and the file is unchanged

#### Scenario: The cap changes while a builder runs
- **WHEN** the same project's `concurrency_cap` is submitted alone
- **THEN** the answer is `200` and the store row carries the new cap

### Requirement: A repository change clears the mirrored items
A successful edit that changes `forge` or `repo` SHALL delete that project's mirrored `item` rows in the same
request, so no item read from the previous repository is planned or dispatched against the new one.

#### Scenario: Repo change before the next tick
- **WHEN** the project had three mirrored items and its `repo` changes
- **THEN** `GET /api/board` reports zero items for the project before any further tick

### Requirement: A delete requires the key to be typed
`DELETE /api/projects/{key}` SHALL require a `confirm` query parameter equal to the key. A missing or different
value SHALL be answered `400` with nothing written.

#### Scenario: A confirmation that does not match
- **WHEN** the request carries `?confirm=wrong`
- **THEN** the answer is `400` and the file and the rows are untouched

### Requirement: A delete is refused while a dispatch is live
A delete SHALL be answered `409` naming the live dispatch and its item, with neither the file nor the rows
changed, and the operator's next step SHALL be named in the message: pause, then retry after the row ends.

#### Scenario: Deleting a project mid-build
- **WHEN** the project has dispatch `12` open on item `#48`
- **THEN** the answer is `409` naming both, and `GET /api/projects` still lists the project

### Requirement: A delete removes the entry and the project's rows
A successful delete SHALL remove the project's entry from the config file and, in one transaction, delete its
`item`, `dispatch`, `verdict`, `snapshot`, `mirror_meta` and `repo` rows. `event` rows SHALL be kept.

#### Scenario: A project with history is deleted
- **WHEN** the operator deletes a project holding four items and two dispatches
- **THEN** the answer is `200` carrying those counts
- **AND** `--print-config` no longer names it and `/api/events` still shows the deletion

### Requirement: A failed delete leaves the project paused, never live
The order SHALL be: refuse on a live dispatch, set `onhold`, remove the file entry, delete the rows. A failure
after the pause SHALL answer `500` naming the step, leave the project `onhold`, and a retry SHALL complete it.

#### Scenario: The file cannot be written
- **WHEN** the config directory is read-only and the operator deletes the project
- **THEN** the answer is `500` naming the file step and the project is `onhold`, so no item is planned
- **AND** a retry after the permission is fixed answers `200`

### Requirement: Pausing is DB-owned and survives a restart
`PUT /api/projects/{key}/onhold` SHALL set the store's `onhold` and SHALL NOT write the config file. A later
`--apply-config` without `--sync-onhold`, and a service restart, SHALL leave the project paused.

#### Scenario: Pause then restart
- **WHEN** the operator pauses a project and the service restarts
- **THEN** the project is still `onhold` and the act loop plans nothing for it

#### Scenario: The file's own onhold wins only when asked
- **WHEN** `--apply-config --sync-onhold` runs against a file that pins `onhold: true`
- **THEN** the project is `onhold` again, and the response says so

### Requirement: The registry shows every project the service knows
`GET /api/projects` SHALL merge the file and the store, reporting per project `source` (`file`, `both` or `db`),
`enabled`, `onhold`, `items`, `dispatches` and `live_dispatch`. A store-only project SHALL be listed and SHALL be
deletable, and an edit of it SHALL answer `409` naming the config file.

#### Scenario: A row the file does not name
- **WHEN** the store holds a project absent from `config.yaml`
- **THEN** the list reports `source: "db"` with its counts, and a `PUT` on it answers `409`

#### Scenario: The list makes no forge call
- **WHEN** `GET /api/projects` runs against a forge that fails every call
- **THEN** the answer is `200` with the same projects and counts

### Requirement: Every registry write is audited
Each successful edit, pause and delete SHALL append one `event` row: actor `board`, kind `project.edit`,
`project.onhold` or `project.delete`, subject `project:<key>`, detail naming the changed fields with their old and
new values.

#### Scenario: An edit lands in the audit trail
- **WHEN** the cap changes from `2` to `3`
- **THEN** `/api/events` holds one `project.edit` row for `project:alpha` whose detail names `concurrency_cap 2 → 3`

### Requirement: The new routes sit behind the existing auth
All three write routes SHALL be served inside the authenticated API group, so a caller with neither a session
cookie nor the operator token can change nothing.

#### Scenario: A remote caller without the token
- **WHEN** `DELETE /api/projects/alpha?confirm=alpha` arrives with no cookie and no token
- **THEN** the answer is `401` and the project is still registered

### Requirement: The Projects surface edits, pauses and deletes
The Projects table SHALL offer Edit, Pause/Resume and Delete per row. The edit dialog SHALL prefill every field
with `key` read-only, the delete dialog SHALL require the key to be typed and SHALL show the counts from the
list, and a success SHALL invalidate the project and board queries.

#### Scenario: The operator edits a rig list
- **WHEN** the operator opens Edit on one row, changes the rigs and saves
- **THEN** the dialog closes, a toast names the project, and the table shows the new rigs without a reload

#### Scenario: A delete shows what it costs
- **WHEN** the operator opens the delete dialog for a project with four items
- **THEN** the dialog names those four items and two dispatches, and the confirm button stays disabled until the
  key is typed
