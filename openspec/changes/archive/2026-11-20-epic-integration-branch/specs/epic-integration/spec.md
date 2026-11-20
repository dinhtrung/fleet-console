## ADDED Requirements

### Requirement: An epic can land on its own branch
When a project sets `epic_branches: true`, an epic without the `epic::direct` label SHALL have its tasks integrated on `epic/<n>-<slug>`, cut from the default branch when the first task is dispatched, and each task's change request SHALL target that branch; a project without the key SHALL behave as today.

#### Scenario: The first task opens the epic branch
- **WHEN** a task of an opted-in epic is dispatched and `epic/<n>-…` does not exist
- **THEN** the branch is created at the default branch's head
- **AND** the task's worktree is cut from that commit and its brief names `epic/<n>-…` as the PR base

#### Scenario: The next task starts from the epic head
- **WHEN** a second task of the same epic is dispatched after the first landed
- **THEN** its base is the epic branch's current head, not the default branch

#### Scenario: One epic opts out
- **WHEN** an opted-in project's epic carries `epic::direct`
- **THEN** its tasks are cut from and target the default branch, as they do today

#### Scenario: The project did not opt in
- **WHEN** `epic_branches` is absent or false
- **THEN** no epic branch is created and every task targets the default branch

### Requirement: The change request carries its base
The app SHALL read every change request's base branch, SHALL merge a task's change request only into its epic's branch, and SHALL refuse a task change request whose base is not that branch with a reason naming both.

#### Scenario: A task on the right base
- **WHEN** a task of an opted-in epic has an accepted change request based on `epic/<n>-…`
- **THEN** the gate merges it into that branch

#### Scenario: A task aimed at the default branch
- **WHEN** that task's change request targets `main`
- **THEN** the gate refuses the merge and the refusal names `epic/<n>-…` and `main`
- **AND** the item carries a note telling the operator to retarget the request

#### Scenario: The epic request is not the gate's to merge
- **WHEN** a change request's head is an epic branch
- **THEN** the gate never merges it, whatever its body or labels

### Requirement: The epic is delivered as one change request
When every task of an opted-in epic is closed, the app SHALL open exactly one change request from the epic branch to the default branch, carrying the task list and `Closes #<epic>`, SHALL never merge it, and SHALL NOT close the epic before that change request is merged.

#### Scenario: The last task closes
- **WHEN** the final open task of an opted-in epic closes and the branch is ahead of the default branch
- **THEN** one non-draft change request `epic/<n>-…` → default is opened with the task list and `Closes #<epic>`

#### Scenario: A request already exists
- **WHEN** the app would open the epic request and one is already open for that branch
- **THEN** it reuses it and opens nothing

#### Scenario: Rollup waits for the operator
- **WHEN** every task is closed but the epic request is not merged
- **THEN** rollup and sweep skip the epic with `integration branch open` and the epic stays open

#### Scenario: A task closed without merging
- **WHEN** one task was closed without a merged change request
- **THEN** the epic request's body lists it under `Not merged`

#### Scenario: The branch is behind
- **WHEN** the epic branch is behind the default branch as the request is opened
- **THEN** the app asks the forge to update the branch
- **AND** a conflict is reported and the request stays unmerged by the app

### Requirement: A missing epic branch stops the work
If the epic branch cannot be created or read while the epic has tasks, the app SHALL refuse the dispatch and name the branch and the forge error; it SHALL NOT fall back to the default branch or recreate the branch over existing work.

#### Scenario: Branch creation fails
- **WHEN** the forge refuses to create `epic/<n>-…`
- **THEN** the task is not dispatched, and the error names the branch and the forge response

#### Scenario: The branch disappeared
- **WHEN** a task is dispatched and the epic branch is absent although earlier tasks were merged into it
- **THEN** the dispatch is refused with a diagnosis, and no branch is created
