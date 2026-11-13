## ADDED Requirements

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
