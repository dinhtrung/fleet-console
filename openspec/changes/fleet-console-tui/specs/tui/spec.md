## ADDED Requirements

### Requirement: The TUI is a second surface, not a second writer

The TUI SHALL read the daemon's HTTP API and SHALL NOT open the database, run a tick, create a queue row or merge. It
SHALL take the token from the same file the daemon checks, and SHALL refuse to run when that file is missing rather than
prompt for a token.

#### Scenario: The daemon is stopped

- **WHEN** the TUI starts while the daemon is down
- **THEN** it draws the last successful frame, marks it `stale: connection refused`, and offers no write key

#### Scenario: A missing token file

- **WHEN** the token file does not exist
- **THEN** the TUI exits with the path it expected and writes nothing

### Requirement: One binary carries both surfaces

The TUI SHALL be a subcommand of the installed binary (`fleet-console tui`), not a second artifact. The release tarball,
its sha256 in `stack.lock` and `install.sh` SHALL NOT change shape because of it, and the build SHALL stay `CGO_ENABLED=0`
and static.

#### Scenario: An existing install

- **WHEN** an operator upgrades with the same tarball-and-sha flow
- **THEN** `fleet-console tui` exists and `--version` reports the same version as the daemon

### Requirement: Three screens and a footer that names the keys

Screens SHALL be projects, items, and one item's drawer. `enter` SHALL descend, `esc` SHALL ascend, `?` SHALL overlay the
keys, `q` SHALL quit. Every screen SHALL show the keys valid in it, and navigation SHALL be complete from the keyboard
with no mouse requirement.

#### Scenario: Descending and returning

- **WHEN** the operator presses `enter` on a project from the projects screen
- **THEN** the items screen lists that project's items, and `esc` returns with the same project selected

### Requirement: The terminal uses the board's sage states

State styles SHALL come from one place mapping the board's semantic tokens — `building`, `review`, `waiting`, `parked`,
`blocked`, `stale` — to lipgloss styles, with a 256-colour fallback when the terminal is not truecolor. No second palette
and no per-user theming SHALL be added.

#### Scenario: A non-truecolor terminal

- **WHEN** `COLORTERM` does not advertise truecolor
- **THEN** the same six states render in 256 colours, and no state is distinguishable by colour alone

### Requirement: Decisions are the only write, and they ask twice

Answering a decision SHALL be the only write the TUI performs: `a` approve, `r` reject, each followed by a confirmation
key. The TUI SHALL send the same request the web board sends, SHALL show the resulting row or the error, and SHALL NOT
offer dispatch, pause, prune or free-text answers.

#### Scenario: Approving a decision

- **WHEN** the operator presses `a` then the confirm key on a pending decision
- **THEN** the decision is answered once, the row disappears from the drawer, and the rig holds the record

#### Scenario: A rejected write

- **WHEN** the API answers 409 because another operator answered first
- **THEN** the TUI shows the conflict and refreshes instead of retrying

### Requirement: Freshness is visible

The TUI SHALL poll `/api/board` every two seconds and SHALL show the time of the last successful read. On an API error it
SHALL keep the last frame, mark it stale with the error, and stop drawing actions that would write.

#### Scenario: The daemon restarts under the TUI

- **WHEN** the daemon is restarted and the next two polls fail
- **THEN** the frame is marked stale, and the next good poll clears it without a restart of the TUI

### Requirement: The model is tested without a terminal

The TUI SHALL be tested by driving `Update` with key messages and comparing `View()` against golden strings at a forced
width and height, against a fake API. Tests SHALL NOT require a PTY.

#### Scenario: A golden frame

- **WHEN** the fake API returns two projects, one paused
- **THEN** the projects screen matches the golden string byte for byte at 100x30
