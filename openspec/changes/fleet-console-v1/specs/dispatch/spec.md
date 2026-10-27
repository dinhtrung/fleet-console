## ADDED Requirements

### Requirement: The work unit is a task

Only an open item that is not an epic SHALL be dispatched. An issue with children SHALL never be handed to a seat,
even when its own fields look dispatchable.

#### Scenario: An epic is skipped

- **WHEN** an open issue has three open children
- **THEN** the plan carries a skip reason naming it as an epic, and only its children are candidates

### Requirement: Blocking looks one way

A task SHALL be blocked when a related item with a **smaller** iid is open on that project. Related items with a
larger iid SHALL NOT block, because the forge's link is symmetric and both directions deadlock a chain.

#### Scenario: A symmetric link

- **WHEN** #110 and #120 are related to each other and both are open
- **THEN** #120 is blocked by #110 and #110 is a candidate

### Requirement: Order by iid, cap by free builders

Candidates SHALL be taken in ascending iid, oldest first across the project, up to the free builders of its rig and
the project's cap.

#### Scenario: Three builders, two free

- **WHEN** five tasks are ready and two builders are free
- **THEN** the two smallest iids are dispatched and the rest carry the reason `no free builder`

### Requirement: Dispatch is idempotent and leased

A dispatch SHALL hold a lease for its project and SHALL abort when the task already has a live worktree, a branch on
origin or a live queue row for its iid. A second run of the same intent SHALL be a no-op.

#### Scenario: A row somebody made by hand

- **WHEN** a task has no dispatch record but a live row's body names its iid
- **THEN** the dispatch aborts with that row named as the reason

### Requirement: A worktree is reused, never deleted

An existing worktree SHALL be reused when its HEAD is the wanted branch, keeping uncommitted work. A worktree on
another branch SHALL be skipped only when it also holds uncommitted changes, and the app SHALL never delete one.

#### Scenario: A seat died mid-run

- **WHEN** a previous dispatch left `wt-app-221` on the right branch with uncommitted files
- **THEN** the new dispatch reuses it and records the reuse in the event trail
