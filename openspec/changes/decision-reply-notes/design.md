# Design — the forge as the reply channel

## D1. The item's conversation is the reply channel, because it is already read

The mirror reads the forge every tick, the app can write a note with the adapter it already has (`Forge.Comment`),
and a comment carries an author and a timestamp the app can trust because the forge assigns them. No new transport,
no second bot, no polling loop: the reply arrives on the same read the tick already performs.

Rejected: a second Telegram bot with its own token. It works, and the owner parked it: a second bot means a second
process to run, a second credential to rotate, and a menu that still cannot be answered from a phone that is not
running Telegram's client. Rejected: relaying through the Hermes gateway. That puts the agent in the decision path,
so a decision's fate depends on an LLM session being awake.

## D2. The note lives on the item, never on the change request

A decision can exist with no change request at all (a parked row, a quota pause), so the change request is not a
surface a decision can rely on. One decision, one note, one thread: the item. The verdict note stays where the
merge gate reads it — the change request — and the two are deliberately different conversations: a verdict judges
code, a decision answers a question about work.

## D3. The reply is an explicit line, not free text

`fleet: <answer>` as the first non-empty line of the comment; everything after it is context nobody parses. The
answer is passed to `rig queue resolve` verbatim, trimmed and capped at 200 characters.

Why a prefix and not "any `accept`/`reject` word": a thread about a rejected pull request contains both words
constantly, and a decision that resolves on prose is a decision nobody made. The escalation prints the exact line,
so the operator copies rather than composes.

An optional second token names the decision (`fleet: accept qitem-20261105062511-…`) for the rare item that has two
open decisions; without it the oldest open decision on that item is answered, and if more than one exists the
reply is refused with a correction note naming the ids (never a guess).

## D4. Authors are an allowlist, defaulting to the repository owner

A comment only answers when its author is in `decision_answerers`; empty means the owner of the project's repository
slug (`dinhtrung` in `dinhtrung/fleet-console`), which is who the escalations are sent to. A bot comment, a
contributor's comment and the app's own notes (which it writes) are ignored — the prefix plus the author rule is what
makes a public item safe to ask a question on.

## D5. One answer, first wins, and the board keeps working

A valid reply calls the same resolution the board's `POST /api/decisions/:id/answer` calls, through the same small
interface. Whichever surface answers first resolves the row; the other sees a terminal row and does nothing, so the
two can never disagree. No second decision store, no state machine of our own.

## D6. Idempotency lives in the audit log

`decision.noted` (subject `#<item>`, detail `<qitem-id>`) records that a note was posted; `decision.answered`
records a resolution and names the comment id. Both are ordinary `event` rows, so the tick's question "is there an
open decision I have not asked about yet?" is one store query, and it survives a restart — a table would hold the
same facts and a migration.

A failed note post writes `decision.note_failed` once per change (the `mirror.note` pattern) and is retried on the
next tick. The escalation is unaffected: the phone message goes out even when the note did not.

## D7. A bad reply earns exactly one correction, keyed by comment

Three cases, three behaviours:

- **No prefix, wrong author, or a comment older than the question** — not a reply. Silence: it is conversation.
- **Prefix, but the decision is not open / already answered** — one note saying so, once per comment id
  (`decision.reply_ignored`).
- **Prefix, but the answer is ambiguous** (two open decisions on the item and no id named) — one note listing the
  open ids, once per comment id.

A correction is never repeated for the same comment, so a persistent typo cannot become a loop; the marker is the
event row, not memory.

## D8. Opt-in, because a note is a write on somebody's item

`decision_notes` defaults to false. When it is on, the app comments on items — an external side effect on a
surface other people read, which an upgraded binary must not start doing by itself. With it off nothing changes:
the escalation and the board behave exactly as today.

## D9. What this is not

- Not an approval menu in Telegram (parked owner decision).
- Not a chat interface: one line, one answer, no LLM in the path.
- Not a replacement for the board: the board stays the fastest surface on the box, and the reason `Quiet()` and the
  decisions endpoint exist.
