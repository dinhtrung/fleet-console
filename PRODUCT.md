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
