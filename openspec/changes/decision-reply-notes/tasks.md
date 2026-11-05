# Tasks — ask on the item, answer on the item

## 0. Ground rules

- Gate: `go build ./... && go vet ./... && go test ./...` and `openspec validate --all --strict`. Real output.
- Fixed vocabulary: config `decision_notes` (bool, default false), `decision_answerers` ([]string, default empty);
  reply prefix `fleet:`; audit kinds `decision.noted`, `decision.answered`, `decision.reply_ignored`,
  `decision.note_failed`; note marker `<!-- fleet-console:decision <qitem-id> -->`.
- The resolution call is the board's: `rig queue resolve`. A second decision store is a review reject.
- No Telegram inline keyboard, no second bot, no polling of the chat.

## 1. The question note (builder 1)

- [x] 1.1 `internal/decisions`: render the note from a decision (question, options, reply line, item link, board
      link) and post it through `Forge.Comment`. Pure renderer, injectable clock.
      Evidence: golden test for the body, including the qitem marker and the reply line; a test for the
      multiple-open-decisions variant that names the id.
- [x] 1.2 Idempotency: `decision.noted` in `event` is the index; a decision with an event is not posted again. A
      failed post writes one `decision.note_failed` per decision per change and is retried next tick.
      Evidence: two ticks → one comment; a failing forge → one event, retry on the next tick, escalation unaffected.
- [x] 1.3 Item number: from `DecisionRaised.Item` when the app raised it; otherwise parsed from the row summary. No
      item number ⇒ one log line and no note (never guess a number).
- [x] 1.4 `cmd/fleet-console/notify.go`: the escalation gains the item URL and the reply line. Evidence: the
      rendered message asserted in the existing fake Bot API test.

## 2. The reply (builder 2, after 1)

- [x] 2.1 `internal/decisions`: read `Forge.Notes(item)` for each open, noted decision; match the first non-empty
      line against `fleet:`; require author ∈ `decision_answerers` (default: the slug owner) and
      `created_at > the question note's`; cap the answer at 200 chars.
      Evidence: a table test — valid, wrong author, no prefix, older comment, prefix with an unknown decision,
      ambiguous item with two open decisions.
- [x] 2.2 Resolve through the rig adapter's `QueueResolve`, post the closing note (`answered: <answer> — <author>`,
      the comment link), record `decision.answered` with the comment id, and never resolve a terminal row.
      Evidence: a fake rig recording one resolve per answer and none for a terminal row; a board-answered row is
      unchanged by a later reply.
- [x] 2.3 Corrections: at most one per comment id, recorded as `decision.reply_ignored`; a re-read posts nothing.
      Evidence: the same comment read twice → one note.
- [x] 2.4 `cmd/fleet-console/main.go`: one call per applied tick, after the act phase, gated by `decision_notes`;
      a failure is one log line and never fails the tick.

## 3. Docs (builder 3, after 1 and 2)

- [ ] 3.1 `PRODUCT.md` + `docs/my-stack.md`: the reply line, where it goes, who may answer, what wins against the
      board, and the two audit kinds. One operator paragraph, no restatement of the requirements.
