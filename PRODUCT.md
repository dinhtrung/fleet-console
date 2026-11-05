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
