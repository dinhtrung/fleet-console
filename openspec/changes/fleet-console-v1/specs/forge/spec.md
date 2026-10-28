## ADDED Requirements

### Requirement: One normalized model over two forges

The app SHALL read and write through a forge interface exposing items, change requests and checks, normalized across
GitHub and GitLab. The decide loop SHALL see only the normalized model.

#### Scenario: The same rule on two forges

- **WHEN** the same task graph is read from GitHub and from GitLab
- **THEN** the decide loop produces the same intents for both

### Requirement: GitHub items, children and dependencies

The GitHub adapter SHALL read issues, their sub-issues as the epic relation, and issue dependencies as `blocked_by`,
with state, labels, title, body and url.

#### Scenario: An epic with children

- **WHEN** a parent issue has three sub-issues and one of them is blocked by another
- **THEN** the parent is an epic, its children are candidates, and the blocked child names its blocker

### Requirement: GitHub change requests and checks

The adapter SHALL read a pull request's head sha, draft state, mergeability, body, closed item and the head's check
runs and commit statuses, including whether a failing check is allowed to fail.

#### Scenario: A docs-only pull request

- **WHEN** a pull request has no checks at all
- **THEN** its head counts as green only because it is docs-only, and the reason is recorded

#### Scenario: An allowed failure

- **WHEN** the only red check is marked allow-failure
- **THEN** the head is not treated as red on that check

### Requirement: Writes are explicit and minimal

The forge adapter SHALL expose exactly four writes: set or clear the marker label, comment, close an item, and merge
a change request at a named sha. No other forge mutation SHALL exist.

#### Scenario: A merge at a stale sha

- **WHEN** the merge is asked for a sha that is no longer the head
- **THEN** the forge refuses it and the app records the refusal rather than retrying at the new head

### Requirement: GitLab parity

The GitLab adapter SHALL provide the same four writes and the same normalized reads, using `related_items` with the
smaller-number rule for blocking and the head pipeline's jobs for checks.

#### Scenario: A pipeline with an allowed failure

- **WHEN** the head pipeline has one failed job marked `allow_failure`
- **THEN** the pipeline reads green for the merge gate and the job id is recorded

### Requirement: Credentials come from the CLI store

Forge access SHALL use the credentials already held by `gh` or `glab`. The app SHALL NOT store a forge token in its
config, its database or its events.

#### Scenario: No token in the repo

- **WHEN** the repository and the database are grepped for a token-shaped value
- **THEN** nothing in either matches
