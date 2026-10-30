## ADDED Requirements

### Requirement: The culture path comes from configuration, never from a request
The app SHALL resolve a project's culture file from `projects[i].culture_file` when set, otherwise from the project's
first rig as `$OPENRIG_SPECS_DIR/rigs/<rig>/CULTURE.md`, and SHALL NOT accept a path, filename or rig name from the
request in either the read or the write route.
#### Scenario: A project that names no culture file
- **WHEN** a project has `culture_file` unset and `rigs: [fleet-console]`
- **THEN** the resolved path is `~/.openrig/specs/rigs/fleet-console/CULTURE.md` and the read response reports
  `source: "derived"` and `rig: "fleet-console"`
#### Scenario: A request that tries to steer the path
- **WHEN** a client sends `?path=`, `?file=` or a body field naming a different file
- **THEN** the app ignores it and reads or writes only the configured path

### Requirement: The culture file is readable with a concurrency token
The app SHALL answer `GET /api/projects/{key}/culture` with the resolved path, provenance, `exists`, `writable`,
`bytes`, `sha256` and `mtime` of the file, and its full content when it exists.
#### Scenario: A culture file that exists
- **WHEN** the file is present and under the size limit
- **THEN** the response carries `exists: true`, the content, and a `sha256` computed over the bytes on disk
#### Scenario: A culture file that does not exist
- **WHEN** the resolved path does not exist
- **THEN** the response is `200` with `exists: false` and the expected path, never a `404`

### Requirement: A write is conditional on the sha the editor saw
The app SHALL require a `base_sha` in every write, SHALL succeed only when it equals the sha of the current file, and
SHALL answer `409` with the current sha and mtime otherwise.
#### Scenario: Two tabs, one file
- **WHEN** the file is edited elsewhere after the editor loaded it and the editor then saves with the old sha
- **THEN** the app answers `409` with `sha` and `mtime` of the current file and writes nothing
#### Scenario: Creating a culture file that is not there
- **WHEN** the file does not exist and the editor sends `base_sha: null`
- **THEN** the write proceeds and the response reports the new sha

### Requirement: A write is atomic, mode-preserving and leaves a backup
The app SHALL write the new content to a temporary file in the target's directory, fsync it, rename it over the
target, preserve the previous file mode, and keep a timestamped copy of the previous content.
#### Scenario: A save over an existing rule file
- **WHEN** a `0644` culture file is replaced
- **THEN** the file is `0644` afterwards, its content is exactly the request body, and a
  `<name>.bak-<UTC timestamp>` file holding the previous bytes exists beside it
#### Scenario: Backups accumulate
- **WHEN** more than five saves have happened
- **THEN** the five newest backups remain and older ones are removed

### Requirement: Refusals are explicit and never partial
The app SHALL refuse a write with `413` for content over 256 KiB, `422` for content that is not UTF-8 text or contains
a NUL byte, `403` for a file or directory that is not writable, and `403` for a path that is a symlink or resolves
outside the rig spec directory — and SHALL leave the file untouched in every one of those cases.
#### Scenario: A read-only culture file
- **WHEN** the file mode is `0444` and a write is attempted
- **THEN** the answer is `403` naming the path and mode, and the bytes on disk are unchanged
#### Scenario: A symlink pointing out of the rig
- **WHEN** the resolved path is a symlink whose target is outside `~/.openrig/specs/rigs/<rig>/`
- **THEN** the answer is `403` with both paths and nothing is written

### Requirement: Every write is recorded as an event
The app SHALL append a `culture.edited` event on a successful write carrying the path, the previous sha, the new sha
and the byte count, visible through the same history the board already serves.
#### Scenario: A successful edit
- **WHEN** a save succeeds
- **THEN** the event table gains one `culture.edited` row naming both shas

### Requirement: The board can read and edit the culture
The app SHALL serve a Culture screen per project showing the resolved path, provenance, sha, size and mtime with the
content rendered, and SHALL offer an edit mode with a monospace textarea, a byte counter, Save and Cancel.
#### Scenario: Opening the screen
- **WHEN** the operator opens `/culture`
- **THEN** the header names the rig, `derived` or `config`, the path, the short sha, the size and the mtime, and the
  content renders as markdown
#### Scenario: Unsaved edits
- **WHEN** the operator tries to leave edit mode or the route with unsaved changes
- **THEN** the app asks before discarding them

### Requirement: A conflict is shown, not resolved silently
The app SHALL render a `409` as a banner naming the sha that is on disk and when it changed, and SHALL offer loading
the current content or overwriting it by resending with the current sha as `base_sha`.
#### Scenario: Saving against a moved file
- **WHEN** a save returns `409`
- **THEN** the banner shows the current sha and mtime, the local text stays in the textarea, and both actions are
  offered

### Requirement: The screen says what a save does not do
After a successful save the app SHALL state that seats already running keep the copy they were launched with and that
the change applies when a seat next starts, and SHALL NOT offer a rig restart from the browser.
#### Scenario: A saved culture change on a live fleet
- **WHEN** the operator saves while three seats are running
- **THEN** the screen confirms the new sha and backup and says the running seats are unaffected until they relaunch

### Requirement: A missing culture file is explained, not created
When the resolved path does not exist, the app SHALL show the expected path, the `culture_file` config key that would
change it, and SHALL NOT create the file until the operator saves from the editor.
#### Scenario: A project with no culture file yet
- **WHEN** the screen loads and `exists` is false
- **THEN** it shows the expected path and the key that sets it, and the fleet keeps running on its startup files
