## MODIFIED Requirements

### Requirement: A missing verdict becomes an operator question
When a change request is open, its item is open, no reviewer row is live and no verdict names the head, the app SHALL raise one operator question for the item, naming the board's Approve/Reject and the item's reply line, and SHALL NOT raise another for the item while that question is still standing, whatever the head.
#### Scenario: The reviewer seat ends its row with no note
- **WHEN** a review row ends and the change request carries no verdict for its head
- **THEN** exactly one queue row with destination `human@kernel` is created
- **AND** no reviewer row is created for that item
#### Scenario: The same head is planned again
- **WHEN** the next tick plans the same item and the head has not moved
- **THEN** no second operator question is raised
#### Scenario: The head moves while the question stands
- **WHEN** the head changes, there is still no verdict for the new head, and the item's no-verdict question is still live
- **THEN** no new operator question is raised
#### Scenario: The head moves
- **WHEN** the head changes, there is no verdict for the new head, and no no-verdict question for the item is live
- **THEN** one new operator question is raised, naming the new sha
