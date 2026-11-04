## ADDED Requirements

### Requirement: A change request with no dispatch row is adopted and merged

When the merge watchdog reads an open change request that closes an open item and the store holds no dispatch row for
that item, the app SHALL adopt the change request and merge it, provided all of these hold: the body says
`Closes #<item>`, the change request is mergeable, its head checks are green, and the head sha carries an `ACCEPT`
verdict read from the change request's own notes.

#### Scenario: A hand-dispatched item merges on the reviewer's verdict

- **WHEN** an item was dispatched by a manual `rig queue create` (so the store has no dispatch row for it), a pull
  request on branch `feature/<item>-...` says `Closes #<item>`, every check is green, and the reviewer posted
  `ACCEPT <head sha>`
- **THEN** the next tick adopts the change request, merges it at that sha, closes the item and clears the dispatch
  marker

#### Scenario: A missing verdict still refuses

- **WHEN** the same change request exists but no `ACCEPT` comment names its head sha
- **THEN** nothing merges, the verdict is still captured if a verdict comment exists at another sha, and the skip line
  names the change request number and the missing fact

### Requirement: Adoption writes its own trail and invents no dispatch

Adoption SHALL record `act/change_request.adopted` with the change request number and the head sha before calling the
forge merge, SHALL write the ordinary `merge` event after it, and SHALL NOT create a `dispatch` row.

#### Scenario: The ledger after an adopted merge

- **WHEN** an adopted merge lands
- **THEN** the ledger holds `change_request.adopted` then `merge` for that item, the `verdict` row carries the
  reviewer's note, and the `dispatch` table still has no row for that item

### Requirement: Adoption refuses the ambiguous cases and names them

Adoption SHALL refuse, naming the reason in the tick's skip line, when a live dispatch exists for the same item on a
different branch, and when the change request's branch does not start with `feature/<item>-`.

#### Scenario: Two branches, one item

- **WHEN** the item has an active dispatch on branch `feature/<item>-app` and a second pull request closes it from
  `feature/<item>-hand`
- **THEN** neither is adopted, the skip line names both branches, and the operator chooses

#### Scenario: A branch that cannot belong to the item

- **WHEN** a pull request closing `#<item>` comes from a branch that does not start with `feature/<item>-`
- **THEN** it is not adopted and the skip line names the branch

### Requirement: A verdict is recorded whether or not the merge proceeds

The app SHALL capture an `ACCEPT` or `REJECT` verdict from the change request notes and store it before any decision
not to merge, including when the item has no dispatch row.

#### Scenario: Reading the ledger after a refused merge

- **WHEN** the reviewer's `ACCEPT` arrives and the merge is refused for a missing fact
- **THEN** the `verdict` row exists and the skip line reports the refusal, not a missing verdict

### Requirement: An adopted merge closes the item like a dispatched one

After an adopted merge the app SHALL verify the item is closed, close it when the body only referenced it, clear the
`status::ongoing` marker and, when a sibling is unblocked, note it.

#### Scenario: The item closes with no dispatch to end

- **WHEN** an adopted merge lands on an item whose pull request body says `Closes #<item>`
- **THEN** the item reads closed, the marker is gone, and no dispatch row is left behind or invented
