# Product

<!-- impeccable:product-schema 1 -->

## Platform

web + a local service

## Stack

Inherited scaffold, user-pinned: **React + Tailwind CSS + Vite** (Vite 8, React 19, TypeScript, Tailwind v4,
TanStack Router / Query / Table). The service, once it exists, is a single **Go** binary with **SQLite** (no cgo)
serving the API and the embedded board.

## What it is

An internal control plane that drives one or more agent fleets (OpenRig rigs) across several projects and
repositories: it mirrors the forge and the rig, decides what is ready by a written contract, dispatches work to
seats, collects review verdicts, merges on four deterministic facts, and keeps every human obligation as a record
with an SLA.

## The rig lifecycle

A rig costs seats whether or not it has work. So the app starts a project's rig when work arrives and stops it once
the rig has sat idle for the configured window — `rig_lifecycle`, off by default so an upgrade changes nothing. A rig
is stopped only when nothing it owns needs a seat, and a row blocked on a person does not count: that rig would idle
for as long as the operator takes. A start lands before its dispatch intent, so no row ever names a seat that does
not exist. Each start and stop writes one log line and one audit event and sends no message, because housekeeping is
neither work nor a decision.

The poll backs off with the fleet: each quiet tick doubles the wait up to `idle_poll_max`, and the first busy one
returns it to `tick_interval`. The trade is explicit — the ceiling is the longest an arriving item waits to be seen,
so it should be set to what the operator will tolerate, not the longest they can. A failing forge read is not
idleness: the wait stays at `tick_interval` while reads fail. Two prefixes carry the whole story in the log:
`rig-lifecycle:` for every start and stop, and `mirror: cadence` for every change of the poll's wait. Requirements:
`openspec/changes/rig-lifecycle/`.

## What the app merges

A change merges only on four deterministic facts, re-read at the moment of the merge: an `ACCEPT` verdict at the
current head sha, green checks (or a docs-only diff), a mergeable change request, and a body that closes the item.
That rule used to require the app to have dispatched the item itself; work started by hand — a queue row created
with `rig queue create` — never merged. It does now: the same four facts are enforced against the change request
when no dispatch row exists, guarded by the branch naming the item (`feature/<item>-`) and by a refusal when a live
dispatch holds the same item on a different branch. Adoption invents no dispatch row and marks a draft ready before
merging, but readiness stays the integrator's job — a human still decides what is worth merging.

## Notices

The phone is told about work only when there is work. A progress notice is suppressed when the snapshot is quiet —
no open epic, nothing in flight, pending or on hold, and no decision waiting on the operator — and the log records
`notify: skipped, no open work: nothing to report` so a reader can tell a deliberate skip from a failed send. A
decision escalation is never muted: an open decision is by definition not quiet. `--notify-report` sends on demand
even when the board is quiet, because an explicit request from the operator is not an unsolicited trigger.

## Surfaces

Seven screens, every one reading the service's API. The rail is Board, Decisions, Seats, Projects, Culture, Audit
and Settings.

- **Board** `/` — the queue: what is running, where, what is blocked and why.
- **Decisions** `/decisions` — what is waiting on the operator, with its SLA.
- **Seats** `/seats` — who is working and on what.
- **Projects** `/projects` — the projects the app drives and their settings.
- **Culture** `/culture` — the rig's culture document and guidance.
- **Audit** `/audit` — the app's own event log, newest first, filterable by actor, kind and subject.
- **Settings** `/settings` — the settings surface's writable keys, with the restart each one needs.

Nine scaffold URLs are gone with no replacement, because their surfaces had no service to read: `/users`,
`/users/:id`, `/roles`, `/roles/:id`, `/organizations`, `/organizations/:id`, `/sessions`, `/api-keys`,
`/feature-flags`. Ten screen files were deleted to get there — Dashboard had no URL of its own, because `/` renders
the board. An operator who bookmarked one of the nine gets the board's not-found, not a mock.

Identity is still the scaffold's. The seven screens read real data, but who you are — the session, its roles and its
permission set — still comes from the mock module in `src/mock`, and `src/auth/session.tsx` is the only consumer of
it. That module stays until identity has a service to read, so "signed in as" is not yet a real fact.
## Answering a decision from the item

A decision is a row waiting on a person, and a person is usually already reading the issue. With `decision_notes`
on, the app writes the question as **one comment on the decision's item**, so the answer can be given where the
question was read instead of only on the board.

The note carries the question, the options when there are any, a link to the item and a link to the board's copy.
It ends with the exact line to type: `fleet: accept`. When the item has more than one open decision the line names
the row's id — `fleet: accept qitem-…` — because `accept` alone would not say which question it answers.

**Who may answer.** The comment's author must be allowed: `decision_answerers` lists the logins, and an empty list
means the owner of the project's repository slug, so a `dinhtrung/fleet-console` project takes replies from
`dinhtrung` and nobody else. A reply from any other login is conversation, not an answer.

**What counts as a reply.** A comment created *after* the question note whose first non-empty line begins with
`fleet:`. The answer is the text after the prefix, trimmed and capped at 200 characters. A comment from before the
question cannot answer it, so an old `fleet: reject` sitting on the issue is inert.

**First answer wins, and the board keeps working.** A valid reply resolves the row through the same call the
board's own answer uses, so the two surfaces cannot disagree — whichever arrives first resolves it and the other
finds a terminal row and does nothing. A late reply after the board answered writes no row state; it is recorded
as `decision.reply_ignored`. The board remains the full surface: `/decisions` lists what is waiting and answers it
with a click, whether or not the notes are configured.

**A bad reply earns one correction, never a loop.** A comment that starts with the prefix but cannot be applied —
the decision is already closed, or the item has two open decisions and the reply names neither — earns exactly one
note saying so, once per comment id. Re-reading the same comment posts nothing, so a persistent typo cannot become
a loop.

The audit log names each step: `decision.noted` when the question was posted, `decision.answered` with the comment
id when a reply resolved it, `decision.reply_ignored` for a correction, and `decision.note_failed` when the forge
refused the note — that last one is retried on a later tick and never holds back the phone message.

`decision_notes` is **off until it is enabled**, because a note is a write on somebody else's item. With it off,
no note is posted, no comment is read for an answer, and the escalation and the board behave exactly as they did
before.

## Who it is for

The person who owns the fleets: they need one screen that answers what is running where, what is blocked and why,
and what is waiting on them — without reading four logs.

## Non-goals

- No model in the dispatch path.
- No replacement for the rig daemon or the forge; it drives both through their CLIs/APIs.
- No code writing, no auto-merge without a verdict.
- No cost dashboards (a report on demand instead).

## Truth

| Question | Source |
|---|---|
| What the system must do | `openspec/changes/fleet-console-v1/specs/**`, then `openspec/specs/**` after archive |
| How it is built | `openspec/changes/fleet-console-v1/design.md` |
| What the workflow is | `docs/my-stack.md` §3 |
| What a surface looks like | `DESIGN.md` (inherited design system) |
