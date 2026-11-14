## ADDED Requirements

### Requirement: A stale verdict shows both shas

An item whose newest verdict names a sha other than the head SHALL show `verdict stale`, the accepted sha and the
current head, and the reviewer who must look again. A stale item SHALL NOT be drawn as `parked`, because parked means
nobody is needed.

#### Scenario: The drawer of a stale item

- **WHEN** an item's verdict is `a1b2c3` and its head is `d4e5f6`
- **THEN** the board and the drawer both name `stale`, `a1b2c3` and `d4e5f6`, and the review is pending
