## ADDED Requirements

### Requirement: Versions come from a lock, not from the box

The installer SHALL read every tool version from `stack.lock` and SHALL fail a step when the installed binary answers
a different version than the lock.

#### Scenario: The distro has an older CLI

- **WHEN** the box already ships a version below the lock
- **THEN** the step installs the pinned version and proves it, or fails naming both versions

### Requirement: Dry-run is the default

Without `--apply` the installer SHALL write nothing, and SHALL print the step list, the pins and the existing
versions it found.

#### Scenario: A first look at a new box

- **WHEN** `./install.sh` runs with no flags
- **THEN** the box is unchanged and the output names every step that would run

### Requirement: Two profiles

The installer SHALL support `fleet` (runtime, rig, harness, forge, tooling, app) and `app` (binary, unit, verify
only), and SHALL refuse a profile step whose prerequisite is absent.

#### Scenario: A box that already runs a rig

- **WHEN** `--profile app` runs on a box with a healthy daemon
- **THEN** the rig, harness and tooling steps are skipped and verify still runs in full

### Requirement: Re-running changes nothing

A second `--apply` on an installed box SHALL be a no-op: no duplicate marker blocks, no rewritten config, no
reinstalled package that already matches the lock.

#### Scenario: The operator runs it twice

- **WHEN** the script is applied twice back to back
- **THEN** the second run reports every step as `already satisfied` and the tree is unchanged

### Requirement: Human steps stop with exit 3

A step that needs a credential the script must not hold SHALL print one copy-pasteable command, exit `3`, and write
no secret to any log.

#### Scenario: A fresh box with no forge login

- **WHEN** the installer reaches the forge login step
- **THEN** it exits 3 with the exact `gh auth login` command and the log contains no token-shaped value

### Requirement: Verify is the only success signal

`--verify` SHALL check pinned versions, daemon state, one real forge read, the app's compat probe, the health
endpoint and the systemd units, and SHALL exit non-zero when any check fails.

#### Scenario: A half-installed box

- **WHEN** `--verify` runs with the app unit stopped
- **THEN** exactly that check fails, its re-run command is printed, and the script exits non-zero

### Requirement: Existing configuration is merged, never replaced

The installer SHALL edit harness, tmux and git configuration through marker blocks or dated backups, and SHALL leave
any rig spec it did not create untouched.

#### Scenario: A box with a hand-made rig

- **WHEN** the installer runs on a box that already has `~/.openrig/specs/rigs/<name>/rig.yaml`
- **THEN** that file is byte-identical afterwards

### Requirement: Boot survives logout

The installer SHALL enable the rig daemon and the app as `systemd --user` units and SHALL enable linger, so both
come back after a reboot with no session open.

#### Scenario: Reboot

- **WHEN** the box reboots with no one logged in
- **THEN** `systemctl --user is-active rig-daemon fleet-console` reports both active
