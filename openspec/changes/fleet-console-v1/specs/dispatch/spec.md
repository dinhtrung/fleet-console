## ADDED Requirements

### Requirement: The work unit is a task

Only an open item that is not an epic SHALL be dispatched. An item with children SHALL never be handed to a seat,
even when its own fields look dispatchable.

#### Scenario: An epic is skipped

- **WHEN** an open issue has three open children
- **THEN** the plan carries a skip reason naming it as an epic, and only its children are candidates

### Requirement: Blocking is one-directional per forge

A task SHALL be blocked only by links the forge defines as directional or by a smaller-numbered related item where
the link is symmetric. The app SHALL NOT apply both rules to one project.

#### Scenario: A symmetric GitLab link

- **WHEN** #110 and #120 are `relates_to` each other and both are open
- **THEN** #120 is blocked by #110 and #110 is a candidate

#### Scenario: A GitHub dependency

- **WHEN** #120 is `blocked_by` #110 and both are open
- **THEN** #120 is blocked regardless of number order, because the link is already directional

### Requirement: Order by number, cap by free builders

Candidates SHALL be taken in ascending item number, up to the free builders summed across the project's rigs and the
project's `concurrency_cap`. Both limits SHALL be applied; the smaller one wins, and the rigs are filled in the order
the project lists them.

#### Scenario: Three builders, two free

- **WHEN** five tasks are ready and two builders are free
- **THEN** the two smallest numbers are dispatched and the rest carry the reason `no free builder`

### Requirement: Dispatch is idempotent against rig rows

A dispatch SHALL abort when the item already has a live contract, a branch on origin or a live rig queue row whose
body names its number. A second run of the same intent SHALL be a no-op naming the existing record.

#### Scenario: A row somebody made by hand

- **WHEN** an item has no dispatch record but a live row's body names its number
- **THEN** the dispatch aborts with that row's id named as the reason

#### Scenario: A second loop instance

- **WHEN** two instances run against one project
- **THEN** the project lease lets exactly one act and the other records `held by <owner>` and writes nothing

### Requirement: A worktree is reused, never deleted

An existing worktree SHALL be reused when its HEAD is the wanted branch, keeping uncommitted work. A worktree on
another branch SHALL be skipped only when it also holds uncommitted changes, and the app SHALL never delete one.

#### Scenario: A seat died mid-run

- **WHEN** a previous dispatch left `wt-app-221` on the right branch with uncommitted files
- **THEN** the new dispatch reuses it and records the reuse in the event trail

### Requirement: A dispatch names the seat and the row it created

Every dispatch SHALL record the seat address, branch, worktree, base sha and the rig queue row id it created, so a
later tick can correlate the row without scanning bodies. The brief SHALL name the rig that owns that seat.

#### Scenario: The row is closed by a seat

- **WHEN** a builder closes its row
- **THEN** the dispatch record shows that row id and the next tick reads its closure rather than guessing

### Requirement: A review goes to the reviewer of the rig that built the item

A review SHALL be requested from the reviewer of the rig whose seat built the item, and the review brief SHALL name
that rig, so a shared reviewer is never assumed. An item with no dispatch SHALL use the project's first listed rig.

#### Scenario: The second rig's builder needs a re-review

- **WHEN** an item was built on `dev-build-1@rig-b` and a moved head needs a re-review
- **THEN** the queue row goes to `dev-review@rig-b` and the brief carries `Rig: rig-b`
