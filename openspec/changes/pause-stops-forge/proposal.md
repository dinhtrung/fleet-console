# Proposal — pause stops all forge traffic

## Why

Pausing a project on the board stops the plan and nothing else. `decide.Decide` returns `nothing` for a paused
project (`internal/decide/decide.go:298`), so no new dispatch is planned — but every other phase still talks to
the forge:

- `Mirror.Tick` reads items, change requests and the git branch index for every project in `specs`
  (`internal/mirror/mirror.go:142`, `:243`, `:297`). The pause flag never reaches the mirror.
- `RunTick` holds no pause guard (`internal/act/tick.go:106`). It reads change-request notes, captures verdicts,
  merges accepted change requests and can post notes. `internal/decide/unread_rig_test.go:110` states this as
  intended: "a paused project still merges its in-flight change requests".
- The serve loop calls `Reconcile` and `RunTick` for every project row and prints `mirror:` and `act:` lines
  (`cmd/fleet-console/main.go:847`, `:948`). A paused project keeps producing both every tick.
- `runHousekeep` reads the forge for every project (`cmd/fleet-console/main.go:1207`).

Observed by the owner: after pausing on the web board, the journal still prints `act: <key> …`,
`rig-lifecycle: …` and `mirror: <key> …`, and the forge token keeps being used for a project nobody is working
on. A revoked or expired PAT on a paused project also still surfaces as errors on the next tick.

## What changes

- A paused project (`onhold`, or `enabled: false` for a block removed from the config) is not a forge client:
  no item or change-request read, no note read, no verdict capture, no merge, no note write, no branch read.
- The board keeps the last mirrored rows and says **paused**, not **stalled**.
- The board's verdict button refuses with `409` while the project is paused.
- Housekeeping skips a paused project: an absent forge read must never read as "nothing is left".
- The fleet-wide rig reads stay, so the lifecycle still sees the seats and may still stop an idle rig.
- Resume wakes the loop; the next tick reads the forge, reconciles what happened and continues.

## Losses, named

- **An accepted change request waits until resume.** A builder already running keeps running; its PR is merged
  on the first tick after the operator resumes.
- **A quota pause inherits the freeze.** `internal/quota/quota.go:96` sets the same `onhold`; there is no source
  column, so a quota-paused project also stops merging until someone resumes it.
- **No Telegram progress digest for a paused project.** The digest is sent from the act phase; a paused project
  no longer enters it. The board remains the surface that says paused.
- **A paused project's worktrees stay on disk** until resume, because housekeeping no longer judges them.
- **The header's forge age stops advancing** while paused. That is the point of the label change, but it means the
  age is no longer a health signal for a paused project.

## Capabilities

New: none.

Modified: `project-registry` (pause semantics), `mirror-loop` (the read pass), `act-tick` (the tick guard),
`review-verdict` (board verdict refusal), `board-navigation` (the header label), `housekeeping` (the reclaim pass).

## Impact

- `internal/mirror/mirror.go` — `Project.Paused`, two guards.
- `internal/act/tick.go` — the `RunTick` guard.
- `cmd/fleet-console/main.go` — `pausedProject`, `projectSources`, the serve loop, `runHousekeep`.
- `internal/web/verdict.go` — the 409.
- `internal/web/mirror_read.go` (plus its caller) — the paused label.
- `internal/decide/unread_rig_test.go` — the comment that documents the old rule.
- `docs/fleet-console.md`, `docs/quickstart.md` — what pause means.
