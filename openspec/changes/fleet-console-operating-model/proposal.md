# Proposal — operating model: one writer, stale verdicts, tiers

## Why

The first real install ran three projects on three rigs and exposed six ways the loop can duplicate work, stall
silently, or dispatch the wrong repository. None of them are visible in the v1 spec set, because v1 assumed one project,
one rig, one instance. The decisions were taken with the owner on 2026-10-29 and are recorded in
`changes/fleet-console-v1/design.md` §17 (D1–D6, with D5 split into D5a/D5b after the owner's correction that a rig is
launched per project folder and a project may have several).

## What changes

| Decision | Capability | Requirement |
|---|---|---|
| D1 | `dispatch` | One instance owns the database |
| D2 | `dispatch`, `console-ui` | A verdict on a moved head is stale, and it asks again |
| D3 | `project-registry` | A project that leaves the file is disabled |
| D4 | `dispatch` | The rig wins every conflict |
| D5a | `project-registry`, `rig-lifecycle` | A project names one or more rigs, in tiers |
| D5b | `dispatch`, `console-ui`, `audit-and-alerts` | Tiers are filled in order; failover follows health |
| D6 | `dispatch` | Dispatch is two-phase and adopted at boot |

## Impact

- No new runtime dependency; the tier list is config, the lock is a file beside the database.
- The single-rig form (`rig: name`) keeps working, so existing installs need no edit.
- The old dispatcher scripts and this app must never tick the same project at once; D1 makes a second *app* instance
  refuse, but a shell script is out of scope and stays the operator's responsibility.

## Order

Apply after `openspec archive fleet-console-v1`: this change MODIFIES two requirements of capabilities that v1 has not yet
archived into `openspec/specs/`, so the validator reports that archiving this change first would be refused.
