## ADDED Requirements

### Requirement: An item labelled as an epic is an epic on the first read

`Items` SHALL classify an item as `epic` when it carries the epic label, even when it has no sub-issues yet. The
epic label SHALL be `type::epic`. An item with no epic label, no sub-issues and a present summary of zero SHALL
remain a `task`.

#### Scenario: The epic is read before its children are linked

- **WHEN** an open issue carries `type::epic` and its `sub_issues_summary.total` is 0
- **THEN** it is returned with `Kind: epic`, the plan skips it, and no builder is dispatched for it

#### Scenario: A task is not an epic

- **WHEN** an open issue carries no epic label, no children, and a present summary of zero
- **THEN** it is returned with `Kind: task`

#### Scenario: The label and the children agree

- **WHEN** an issue carries `type::epic` and has two sub-issues
- **THEN** it is an epic and both children get that issue as their parent

### Requirement: A task costs no sub-issue call

The adapter SHALL skip the `sub_issues` read for an item whose present summary reports zero children, and SHALL read
it for an epic, so classification adds no per-task request.

#### Scenario: Reading a repository of tasks

- **WHEN** the open list carries summaries of zero for every item and no epic labels
- **THEN** no `sub_issues` call is made

#### Scenario: Reading a label-epic with no children yet

- **WHEN** an item carries the epic label and the summary reports zero
- **THEN** exactly one `sub_issues` call is made for it, its result is empty, and the item is still an epic
