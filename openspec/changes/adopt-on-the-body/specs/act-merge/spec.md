## ADDED Requirements

### Requirement: The body adopts an item, the branch only agrees
The adoption gate SHALL take the item from `Closes #<n>` in the change request body. It SHALL merge when the branch
names that same item or names none, and SHALL refuse when the branch names a different item, reporting both numbers.

#### Scenario: A branch that names no item
- **WHEN** a reviewed change request closes #450 and sits on `fix/delete-project-verdicts-by-project-id`
- **THEN** it merges, and one warning names the convention and the branch

#### Scenario: A branch that names the item
- **WHEN** the branch is `feature/450-clean-up` and the body closes #450
- **THEN** it merges with no warning

#### Scenario: A branch that names another item
- **WHEN** the branch is `fix/438-follow-up` and the body closes #451
- **THEN** it is refused with a reason naming #438 and #451, and nothing is merged

### Requirement: The warning is written once per head
The warning for a branch that names no item SHALL be written once for each item and head, not on every tick.

#### Scenario: A change request waiting an hour
- **WHEN** the same change request sits unmerged for several ticks
- **THEN** one warning is written and later ticks are silent
