## ADDED Requirements

### Requirement: One static binary carries the app

The release artifact SHALL be a single executable built with `CGO_ENABLED=0`, with the board embedded and the
timezone database embedded, and SHALL run on a box with no Go toolchain and no Node runtime.

#### Scenario: Copy it to another box

- **WHEN** the binary is copied to a second fleet box and started
- **THEN** it serves the board and opens its SQLite database with nothing else installed

### Requirement: The binary names its host dependencies

The binary SHALL check for `rig`, a forge CLI, `git` and `tmux` at startup and SHALL name the missing one, keeping
the act loop off until it is present.

#### Scenario: A box without a forge CLI

- **WHEN** the binary starts where `gh` is absent
- **THEN** startup fails with the missing binary named, and nothing is dispatched

### Requirement: Version and compat are machine-readable

`--version --json` and `--compat` SHALL print structured output: version, commit, build time, rig floor, observed rig
version, and the result of the two parsed shapes.

#### Scenario: An installer checking the pair

- **WHEN** the installer runs `--compat`
- **THEN** it gets exit 0 and JSON it can compare against `stack.lock`

### Requirement: The image is an exception with declared mounts

An optional container image SHALL include the host tools the binary drives, and its build file SHALL state which host
paths and sockets it must see to reach a running rig. The default fleet-box deployment SHALL remain the bare binary
under systemd.

#### Scenario: A container that cannot see the host rig

- **WHEN** the image runs without the rig state mounted
- **THEN** its startup check reports the rig as unreachable instead of driving a private daemon

### Requirement: Upgrade is reversible

An upgrade SHALL stop the unit, back up the database to a dated file, replace the binary, run migrations, restart and
verify; a failed verify SHALL restore the previous binary and leave the backup in place.

#### Scenario: A migration that fails

- **WHEN** the new binary exits non-zero during migration
- **THEN** the previous binary is running again and the log names the backup file
