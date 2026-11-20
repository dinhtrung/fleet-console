## ADDED Requirements

### Requirement: A pause refuses a board verdict

The board verdict route SHALL answer 409 with `project is paused; resume it to post a verdict` when the item's
project is paused, and SHALL write nothing: no note, no ready mark, no mirror wake.

#### Scenario: Accept on a paused project

- **WHEN** the operator posts accept for an item whose project is paused
- **THEN** the response is 409, no note is posted, and the mirror loop is not woken

#### Scenario: Resume, then accept

- **WHEN** the project is resumed and the operator posts the same verdict
- **THEN** the note is posted as usual and the loop is woken
