## ADDED Requirements

### Requirement: The New project dialog fits the viewport
The New project and Edit project dialog SHALL keep its title and its Create/Save and Cancel controls inside the viewport at 1280×720 and 390×844, SHALL scroll only the form body between them, and SHALL keep working while the page behind it is scroll-locked by the modal.

#### Scenario: A 720px-tall laptop
- **WHEN** the operator opens New project at 1280×720
- **THEN** the dialog's bounding rectangle is inside the viewport
- **AND** the Create project button is visible without scrolling the dialog

#### Scenario: A phone
- **WHEN** the operator opens New project at 390×844
- **THEN** the dialog's bounding rectangle is inside the viewport and the footer is visible

#### Scenario: The last field is reachable
- **WHEN** the operator scrolls the dialog body to `paths.worktrees`
- **THEN** the header and footer are still visible
- **AND** the page behind the dialog has not scrolled

### Requirement: Long preflight guidance is disclosed, not dumped
The create dialog's live preflight SHALL render as one summary line naming its state and every failing check id, SHALL render the per-check detail, fix and command only after the operator expands it, and SHALL keep Create project enabled in both states.

#### Scenario: A draft with failing checks
- **WHEN** the key settles and the preflight answers `would not be ready` with two failing checks
- **THEN** the panel renders one summary line naming both check ids
- **AND** Create project stays enabled

#### Scenario: The operator expands the panel
- **WHEN** the operator activates `Show details`
- **THEN** the four check rows render with their detail, fix sentence and command
- **AND** the footer is still inside the viewport

#### Scenario: Nothing is checked yet
- **WHEN** the key field is empty
- **THEN** no preflight panel renders
