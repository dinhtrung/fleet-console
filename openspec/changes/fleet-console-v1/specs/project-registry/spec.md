## ADDED Requirements

### Requirement: A project is a row, not a script

A project SHALL be one row naming its forge kind and project id, its default branch, one or more repos with a local
path and worktree root, its rig name and the rig template that generates it. Onboarding SHALL be that row, with no
new timer, log or state file.

#### Scenario: Two projects on one box

- **WHEN** a second project is added with its own repo and rig
- **THEN** both dispatch, and no worktree path or branch is shared between them

#### Scenario: A project with two repos

- **WHEN** a project owns `app` and `docs` and a task lands in `docs`
- **THEN** its worktree is cut from the `docs` repo and the app checkout is untouched

### Requirement: Capacity comes from the rig

Free builders SHALL be computed from the rig's own state — its seats minus rows that are working, claimed or in
progress — capped by the project's `concurrency_cap`. Capacity SHALL NOT be a configured number.

#### Scenario: A seat parked on a prompt is not free

- **WHEN** a builder's seat reports `needs_input` on a permission prompt
- **THEN** it counts as busy and a decision is raised, because no tick can clear a menu

### Requirement: Pause and kill switch

A project SHALL be pausable, globally and per project. A paused project SHALL keep mirroring and raising decisions
and SHALL write nothing to the forge, rig or git.

#### Scenario: Paused means read-only

- **WHEN** a project is paused while a task is ready
- **THEN** the board still shows the task and its reason, and no worktree, row or label is written

### Requirement: Every screen stamps its source

A screen reading mirrored or cached state SHALL show when that source was last refreshed. An empty board SHALL never
be the result of a failed fetch alone.

#### Scenario: The forge is unreachable

- **WHEN** the forge read fails for a project
- **THEN** the board shows the last successful sync time and marks the project stale, instead of rendering no tasks

### Requirement: The registry owns the rig, not only its name

A project row SHALL name the rig template and the inputs that generate its rig; a rig name that no template
produced SHALL be reported as unmanaged rather than driven.

#### Scenario: A hand-made rig

- **WHEN** a project points at a rig whose spec the app did not render
- **THEN** the board marks it unmanaged and the act loop refuses to dispatch to it
