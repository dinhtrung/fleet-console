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
