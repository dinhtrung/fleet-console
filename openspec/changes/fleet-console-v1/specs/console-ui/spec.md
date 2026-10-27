## ADDED Requirements

### Requirement: The board names why a task is not moving

Every task on the board SHALL carry its blocking reason — waits on another iid, parked, red gate, no free builder,
paused — derived from stored facts, and a task with no reason SHALL be a failure the board reports.

#### Scenario: The three kinds of idle

- **WHEN** the fleet is idle because a verdict is parked
- **THEN** the board says so on the task, and does not read as "everything is done"

### Requirement: A decision is a record, not a message

An obligation waiting on a human SHALL be a stored row with an owner, a question, the options that clear it and a
deadline. It SHALL survive a restart, and answering it SHALL write the answer to the row or issue it came from.

#### Scenario: A restart does not lose the obligation

- **WHEN** the app restarts while a parked decision is unanswered
- **THEN** the decision is still on the queue with its age and SLA

### Requirement: One screen answers who checked a change

A task's drawer SHALL show its brief, branch, worktree, merge request, the head pipeline's jobs, the verdict text and
the event trail, so the review behind a merge is readable without reading a log file.

#### Scenario: A merged task

- **WHEN** a merged task is opened
- **THEN** the drawer names the verdict, its sha and the jobs the merge depended on
