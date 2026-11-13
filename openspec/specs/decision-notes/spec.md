# decision-notes Specification

## Purpose
A raised decision is asked as a note on its item, and a reply under that note resolves it on the next tick with one correction for a bad reply.

## Requirements

### Requirement: A raised decision is asked as one note on its item

When the app raises a decision and `decision_notes` is enabled, it SHALL post one comment on the decision's item
before or with the escalation, and record a `decision.noted` event carrying the item and the decision id. It SHALL
post no second note for the same decision id.

#### Scenario: A decision about an item with no change request

- **WHEN** the app raises a decision on item #178 and no change request exists
- **THEN** one note is posted on issue #178, one `decision.noted` event names `#178` and the qitem id, and no note
  is attempted on a pull request

#### Scenario: The tick runs twice

- **WHEN** the next tick finds the same decision still open
- **THEN** no second note is posted, because the event log already says it was asked

#### Scenario: The note cannot be posted

- **WHEN** the forge write fails
- **THEN** one `decision.note_failed` event is written for that decision, the escalation still goes out, and the
  post is retried on a later tick

### Requirement: The note states the question, the reply line and the links

The note SHALL carry the decision's question, the options when the decision has them, the exact reply line
`fleet: <answer>` (with the qitem id when the item has more than one open decision), a link to the item, and a link
to the board's decision. It SHALL NOT repeat anything the item does not already show.

#### Scenario: Reading the note on a phone

- **WHEN** the operator opens the item from the notification
- **THEN** the note tells him what is being asked, what to type, and where the board's own copy is

### Requirement: The escalation carries the link and the reply line

The Telegram decision message SHALL include the item URL and the same reply line as the note, and SHALL NOT claim a
button or a menu the app cannot receive.

#### Scenario: A decision is escalated

- **WHEN** a decision is sent to the phone
- **THEN** the message names the item link and the line to reply with, and no inline keyboard is sent

### Requirement: A reply is a comment created after the note, from an allowed author, starting with the prefix

A comment SHALL answer a decision only when its author is allowed, it was created after the question note, and its
first non-empty line begins with `fleet:`. The answer SHALL be the text after the prefix, trimmed and capped at 200
characters. Any other comment SHALL NOT resolve anything.

#### Scenario: A valid reply

- **WHEN** the allowed author comments `fleet: accept` on the item thirty seconds after the note
- **THEN** the decision is resolved with `accept`

#### Scenario: A reply from someone else

- **WHEN** a contributor without permission comments `fleet: accept`
- **THEN** nothing is resolved and no correction note is posted, because the prefix rule is not met by that author

#### Scenario: Ordinary conversation

- **WHEN** a comment says `looks good to me, accept it` with no prefix
- **THEN** nothing is resolved

#### Scenario: A comment from before the question

- **WHEN** the item already carried a `fleet: reject` comment from yesterday
- **THEN** it does not answer today's decision

### Requirement: A valid reply resolves the decision exactly as the board does

The app SHALL resolve the row through the same call the board's answer uses, SHALL post one closing note naming the
answer and its author, and SHALL record `decision.answered` with the comment id. A decision already resolved by any
surface SHALL NOT be resolved again.

#### Scenario: The reply wins the race

- **WHEN** the operator replies `fleet: accept` and the row is still open
- **THEN** the row is resolved, the closing note is posted, and the decision disappears from `/decisions`

#### Scenario: The board answered first

- **WHEN** the reply arrives after the row was resolved on the board
- **THEN** nothing is written to the row and one `decision.reply_ignored` event records the late comment

### Requirement: A bad reply earns one correction, never a loop

A comment that starts with the prefix but cannot be applied SHALL earn exactly one correction note, recorded as
`decision.reply_ignored` with the comment id, and SHALL NEVER be answered twice.

#### Scenario: An ambiguous item

- **WHEN** the item has two open decisions and the reply names neither
- **THEN** one note lists the open ids to choose from and nothing is resolved

#### Scenario: The same comment is read again

- **WHEN** the next tick reads that comment again
- **THEN** no second correction is posted, because the comment id is already recorded

### Requirement: The feature is off until it is enabled

When `decision_notes` is false the app SHALL post no note, SHALL read no comment for an answer, and SHALL leave the
escalation and the board unchanged. `decision_answerers` SHALL default to the owner of the project's repository slug,
and a comment from any other login SHALL NOT answer a decision.

#### Scenario: An upgraded box

- **WHEN** the configuration has no `decision_notes`
- **THEN** no decision note is written and every existing decision path is unchanged

#### Scenario: The default author rule

- **WHEN** `decision_answerers` is empty and the project is `dinhtrung/fleet-console`
- **THEN** comments by `dinhtrung` can answer and comments by any other login cannot
