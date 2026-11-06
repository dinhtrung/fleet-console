# Proposal — ask on the item, answer on the item

## Why

A decision is a rig queue row blocked on a person. Two surfaces exist today: the board
(`/decisions`, `POST /api/decisions/:id/answer`, `internal/web/fleet.go:539-575`) and a Telegram escalation
(`alert.Escalator`, `cmd/fleet-console/notify.go:93`).

Neither reaches the operator where he actually is. The board is loopback-only, and the escalation is text with no
way back: Telegram allows one `getUpdates` consumer per bot, the Hermes gateway owns it, so an app-side menu answers
`409 Conflict` and inline buttons render dead. The owner parked the second-bot-vs-relay question on 2026-11-04,
which leaves every decision waiting for an ssh session or a browser on the box.

Meanwhile the forge is the one channel that already works in both directions and is already read every tick: the
mirror reads an item's conversation (`Forge.Notes`, `internal/forge/forge.go:198`) and the app can write one
(`Forge.Comment`). A decision can therefore be *asked* as a note on its item, and answered by replying to that note
— from a phone, in the same place the work is described, with the whole thread as the record.

## What changes

- When the app raises a decision it posts **one note on the item** carrying the question, the options, the exact
  reply line, and links to the item and the board, and records `decision.noted` in the audit log.
- The Telegram escalation keeps working and now carries the **item link** and the same reply line — it becomes a
  pointer, not a channel.
- On every applied tick the app reads the item's notes for each open, noted decision and looks for a reply that is
  a comment **created after the question**, from an **allowed author**, whose first non-empty line begins with the
  reply prefix.
- A valid reply resolves the row with the same call the board uses (`rig queue resolve`), posts one closing note
  naming the answer and its author, and records `decision.answered`. There is no second decision store.
- Nothing is inferred from ordinary conversation: a comment without the prefix is never an answer.

## Losses, named

- **A reply is public to everyone who can see the item.** On a public repository a decision's question is public
  text. The note therefore never repeats anything the item does not already show: no credentials, no hostnames, no
  paths outside the repository.
- **The reply is a sentence, not a click.** A typo cannot be undone by editing the comment: the first valid reply
  wins and the row is terminal. The escalation states the exact line so it can be copied.
- **Note traffic becomes real.** Every decision adds two comments to the item (question, resolution) and the reply
  is a third. That is the price of a channel that survives a phone.
- **A forge outage now delays decisions too.** The board answer still works; the note path degrades to "the
  escalation was sent, the reply will be read when the forge returns", one log line per change.

## Capabilities

### New Capabilities

- `decision-notes`: the question note, its idempotency and content, the reply syntax and author rule, the
  resolution path, and the one correction note a bad reply earns.

## Impact

- `internal/decisions` (new) — note rendering, reply matching, resolution; pure where possible, the forge and the
  rig behind small interfaces so both sides are testable with fakes.
- `cmd/fleet-console/notify.go` — the escalation text gains the item link and the reply line.
- `cmd/fleet-console/main.go` — one call per applied tick, after the act phase.
- `internal/config` — `decision_notes` (default off) and `decision_answerers` (default: the project's repo owner).
- `internal/store` — no new table: `decision.noted` and `decision.answered` are ordinary `event` rows, and the
  event log is the index the tick reads.
- `docs/` — the operator's side: what to reply, where, and what wins.
