# settings Specification

## Purpose
The settings surface reads the effective configuration, writes only allowlisted keys under guard, and says when a change needs a restart.

## Requirements

### Requirement: The settings surface reads the effective configuration

`GET /api/config` SHALL return every effective setting with the source it came from (flag, environment, file or
default), redacted the way `--print-config` redacts it. The `/settings` screen SHALL render those values instead of
any browser-local state.

#### Scenario: Reading the configuration

- **WHEN** the operator opens `/settings`
- **THEN** each setting shows its value and its source, and no credential value appears in the response or the page

#### Scenario: A value that came from the environment

- **WHEN** `tick_interval` is set both in the file and in the environment
- **THEN** the screen shows the winning value and names the environment as its source

### Requirement: Only allowlisted settings are writable

`PUT /api/config` SHALL accept only `tick_interval`, `notify_interval`, `act` and the three `rig_lifecycle` keys.
Any other key SHALL answer 400 naming it. `projects`, the credentials and the binary paths SHALL NOT be writable
through this route.

#### Scenario: An editable key

- **WHEN** the body sets `rig_lifecycle.enabled: true`
- **THEN** the value is written to the file and the response reports the change

#### Scenario: A read-only key

- **WHEN** the body carries `listen`
- **THEN** the response is 400 naming `listen`, and the file is unchanged

#### Scenario: An unknown key

- **WHEN** the body carries a key the file shape does not have
- **THEN** the response is 400 and the file is unchanged

### Requirement: A write is guarded, validated and atomic

A write SHALL be refused when the caller's expected hash does not match the file on disk, SHALL be validated as a
whole document before it is applied, SHALL preserve the operator's comments, and SHALL replace the file atomically.

#### Scenario: A stale read

- **WHEN** the file changed after the screen loaded it
- **THEN** the response is 409 and the file keeps the operator's edit

#### Scenario: A write that would break the boot

- **WHEN** the candidate sets `idle_poll_max` below `tick_interval`
- **THEN** the loader's validation refuses it, the response names the key, and the file is untouched

#### Scenario: Unchanged values

- **WHEN** the body sets a value the file already has
- **THEN** the response reports no change and the file's bytes are identical

### Requirement: A saved change says it needs a restart

When at least one value changed, the response SHALL report that a restart is required and the screen SHALL show the
command that restarts the service. The app SHALL NOT restart itself.

#### Scenario: Saving a new interval

- **WHEN** `tick_interval` changes from 60s to 30s
- **THEN** the response reports the change and the restart requirement, and the screen shows
  `systemctl --user restart fleet-console`
