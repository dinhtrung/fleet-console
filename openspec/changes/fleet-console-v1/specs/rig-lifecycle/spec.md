## ADDED Requirements

### Requirement: A rig is generated from its project row

A project's rig SHALL be rendered from one template with explicit inputs — rig name, per-seat cwd, runtime, model,
account env, builder count, guidance and culture — into `~/.openrig/specs/rigs/<rig>/rig.yaml`. No rig spec SHALL be
authored by hand for a managed project.

#### Scenario: Onboarding a project

- **WHEN** a project row is added with a repo path and a builder count
- **THEN** the app renders the spec, validates it and launches the rig without a human editing YAML

### Requirement: Validate and plan before launch

Before launching, the app SHALL run `rig spec validate` and `rig up --plan`, and SHALL refuse to launch when either
reports an error. A warning SHALL be recorded on the event.

#### Scenario: A bad runtime name

- **WHEN** the template names a runtime the box does not support
- **THEN** the plan fails the launch, the event carries the plan's message, and no seat is started

### Requirement: A launch is verified, not assumed

A launch SHALL be complete only when every declared seat reports running in `rig ps --nodes --rig <rig> --json`. A
missing or stopped seat SHALL fail the launch with that seat named.

#### Scenario: One seat refuses to start

- **WHEN** two seats run and the reviewer does not
- **THEN** the launch is reported failed naming the reviewer, and dispatch stays disabled for that project

### Requirement: Growth is written back to the spec

Adding a seat SHALL use `rig grow` and SHALL then export the running topology back to the spec, so a later restore
reproduces what is running.

#### Scenario: A third builder

- **WHEN** a third builder is added to a running rig
- **THEN** the exported spec validates and names three builders

### Requirement: Removal frees the name

Removing a rig SHALL use `rig down --delete` and archive its spec directory. `rig archive` SHALL NOT be used, because
it keeps the name taken.

#### Scenario: Reusing a rig name

- **WHEN** a project is removed and a new project wants the same rig name
- **THEN** the name is free and the new spec launches

### Requirement: Seat credentials and MCP are seeded before launch

The app SHALL seed each seat's agent directory with its provider key and its MCP entries before the seat starts, and
SHALL NOT print any seeded value.

#### Scenario: A seat with no key

- **WHEN** a seat's agent directory has no provider credential
- **THEN** the launch refuses before starting that seat, naming the missing file

### Requirement: Isolation is one rig per project

Each project SHALL drive its own generated rig. A rig shared by two projects SHALL be reported as unmanaged and
SHALL NOT be dispatched to.

#### Scenario: Two projects on one rig

- **WHEN** two project rows name the same rig
- **THEN** the board reports the conflict and neither dispatches until it is resolved
