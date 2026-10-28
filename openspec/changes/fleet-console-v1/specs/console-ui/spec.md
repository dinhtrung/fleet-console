## ADDED Requirements

### Requirement: The board names why a task is not moving

Every task on the board SHALL carry its blocking reason — waits on an item, parked, red gate, no free builder,
paused — derived from stored facts, and a task with no reason SHALL be a failure the board reports.

#### Scenario: The three kinds of idle

- **WHEN** the fleet is idle because a verdict is parked
- **THEN** the board says so on the task, and does not read as "everything is done"

### Requirement: A decision is a rig record, not an app record

An obligation waiting on a human SHALL be a rig queue row addressed to a person, carrying the question, the
options, an evidence reference and a tier deadline. The app SHALL NOT keep a parallel decision table.

#### Scenario: A restart does not lose the obligation

- **WHEN** the app restarts while a parked decision is unanswered
- **THEN** the row is still on the rig queue with its age and deadline, and the board reads it back

#### Scenario: The answer is durable

- **WHEN** the owner answers on the board
- **THEN** the answer is written with `rig queue resolve` and the row's transition log names it

### Requirement: One screen answers who checked a change

A task's drawer SHALL show its brief, branch, worktree, change request, the head's checks, the verdict text and the
event trail, so the review behind a merge is readable without reading a log file.

#### Scenario: A merged task

- **WHEN** a merged task is opened
- **THEN** the drawer names the verdict, its sha and the checks the merge depended on

### Requirement: shadcn/ui is the component library

Interactive primitives SHALL come from shadcn/ui generated into `src/components/ui`, committed with
`components.json`. A screen SHALL NOT introduce a second component library.

#### Scenario: A new dialog

- **WHEN** a screen needs a dialog
- **THEN** it imports the shadcn dialog from `src/components/ui`, and no bespoke dialog file is added

### Requirement: Sage is the only appearance

The board SHALL render one appearance, `sage`. The light, dark, dusk and ember token blocks, their registry entries
and the appearance switcher SHALL be removed, and no `data-theme` variant SHALL remain in the source.

#### Scenario: A stale stored choice

- **WHEN** a browser holds `fleet-console.appearance = "ember"`
- **THEN** the board renders sage and clears the stale value, with no flash of another palette

### Requirement: Semantic tokens bridge, not palette values

shadcn's semantic variables (`--background`, `--foreground`, `--primary`, `--border`, `--ring`, `--radius`) SHALL
map onto the sage palette roles, and a generated component SHALL NOT carry raw palette hex values.

#### Scenario: Reviewing a generated component

- **WHEN** a component from the registry is added
- **THEN** its colours resolve through the bridge variables and a grep for palette hex in the diff returns nothing

### Requirement: Replaced primitives are deleted

Where shadcn replaces an inherited primitive (button, dialog, menu, field, toast), the inherited file SHALL be
deleted and every import updated in the same change. Two implementations of one control SHALL NOT coexist.

#### Scenario: The old menu still imported

- **WHEN** a screen still imports the deleted menu
- **THEN** `npx tsc -b` fails before the build, naming the file
