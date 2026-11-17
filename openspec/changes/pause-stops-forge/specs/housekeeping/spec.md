## ADDED Requirements

### Requirement: A paused project is not a reclaim candidate

The reclaim pass SHALL skip a paused project entirely: it SHALL NOT read the project's forge, SHALL NOT remove or
propose a worktree, and SHALL write one line naming the pause.

#### Scenario: Housekeeping while paused

- **WHEN** housekeeping runs and a project is paused
- **THEN** no worktree is removed or proposed for it, and the journal carries `housekeep: <key>: paused, skipped`

#### Scenario: The read that did not happen

- **WHEN** a paused project's worktree looks unreferenced in the cache
- **THEN** it is not removed, because no forge read proved it is unused
