# Tasks — pause stops all forge traffic

## 0. Ground rules

- **Build gate:** `CGO_ENABLED=0 go build -trimpath -tags embedboard ./cmd/fleet-console` (the board's dist is
  already committed; no `npm run build` is needed for a Go-only change) and `go test ./...`. A PR is green only
  when both pass.
- **Fixed vocabulary — do not invent a second name for any of these:**
  - `mirror.Project.Paused bool` — the read pass's pause flag.
  - `func pausedProject(p store.ProjectRow) bool` — `p.OnHold || !p.Enabled`, defined once in
    `cmd/fleet-console`.
  - Journal line for housekeeping: `housekeep: <key>: paused, skipped`.
  - Header label: `paused — no forge reads`; state `paused` (never `stalled` for a paused project).
  - Verdict refusal: HTTP `409`, body message `project is paused; resume it to post a verdict`.
  - No schema change. No `onhold_source` column. No new config key.
- **What "paused" means here:** the store row is `onhold = 1`, or `enabled = 0` (a config block removed). Both are
  pause. `internal/web/fleet.go:83` already treats them as one state.
- **What the pause must NOT touch:** a running builder, the rig daemon, the rig queue/roster reads (fleet-wide),
  the lifecycle's stop rule, and the mirrored rows already in the store.
- **Stop and report instead of inventing.** If a deliverable below cannot be done without changing the pause
  semantics, a store schema, or the lifecycle rule, stop and write the question on the issue.

## 1. Mirror: a paused project is not read (T1)

- [x] 1.1 `internal/mirror/mirror.go`: add `Paused bool` to `Project`, with the comment stating that a paused
      project keeps its rig facts and loses its forge and git reads.
- [x] 1.2 In `Tick`, guard the forge block (`project.Forge != nil`, around `:243`) and the git block
      (`project.Git != nil`, around `:297`) with `!project.Paused`. Do not skip the status/roster writes and do
      not skip the queue/seat reads.
- [x] 1.3 Assert in a test that a paused tick calls neither source: set `fake.ItemsErr` and `fake.RequestsErr`,
      pass a git runner that counts calls and would answer a branch, and assert the previous item rows, the
      `status` forge stamp and the `branches` snapshot are unchanged.
- [x] 1.4 Evidence: `go test ./internal/mirror -run 'TestPausedProjectReadsNothingFromTheForge' -v`.

## 2. Act: a paused project's tick does nothing (T2)

- [ ] 2.1 `internal/act/tick.go`: in `RunTick`, after `report` is built and **before** `in.Gate.Check()`, return
      the empty report when `in.Project.OnHold || !in.Project.Enabled`.
- [ ] 2.2 Test both flags (`onhold`, `enabled:false`) with a live dispatch row, a mergeable change request carrying
      `Closes #5` and an ACCEPT note, plus a plan carrying a dispatch intent: assert no `fake.Writes`, no
      `fake.CRNotesAsked`/`NotesAsked`, no queue create, no merges and no dispatches.
- [ ] 2.3 Update the comment in `internal/decide/unread_rig_test.go:107-112`: the old text says a paused project
      still merges. The plan rule is unchanged; the sentence about merging is now false.
- [ ] 2.4 Evidence: `go test ./internal/act -run 'TestRunTickPausedProjectDoesNothing' -v` and
      `go test ./internal/decide`.

## 3. Wiring: the loop and housekeeping honour the pause (T3 — blocked by T1, T2)

- [ ] 3.1 `cmd/fleet-console/main.go`: add `pausedProject(store.ProjectRow) bool` returning
      `p.OnHold || !p.Enabled`, and set `Paused: pausedProject(project)` in `projectSources`.
- [ ] 3.2 Serve loop (`cmd/fleet-console/main.go:847`): after `reads[project.ID] = projectRead`, `continue` for a
      paused project — no reconcile, no plan build, no `mirror:` line for it. Its rig facts stay in `reads` so the
      lifecycle still decides on them.
- [ ] 3.3 Act loop (`cmd/fleet-console/main.go:930`): `continue` for a paused project before `RunTick`, so no
      `act:` line, no verdict capture, no merge, no reply read, no progress digest.
- [ ] 3.4 `runHousekeep` (`cmd/fleet-console/main.go:1207`): skip a paused project and write the fixed journal
      line. This is the safety half: with no forge read the change-request list is empty, and an empty list is
      what authorises a worktree removal.
- [ ] 3.5 Evidence: `go test ./cmd/fleet-console`, plus a unit test for `pausedProject` (all four flag
      combinations) and a `projectSources` test proving a paused row yields `Paused: true` (and an enabled row
      false) — use an empty `forge.NewFake`; no forge call is needed to build a spec.

## 4. Web: the board refuses verdicts and says paused (T4)

- [ ] 4.1 `internal/web/verdict.go` `ItemVerdict`: after the project row is found and before any forge read,
      answer `409` with the fixed message when `projectPaused(project)`. No note, no `Ready`, no `wake`.
- [ ] 4.2 `internal/web/mirror_read.go` and its caller: for a paused project return label
      `paused — no forge reads` and state `paused`, whatever the stamp's age; leave the never-read case alone.
- [ ] 4.3 Tests: a verdict request on an `onhold` project → 409 and an empty `fake.Writes`/`NotesAsked`; the same
      request after resume → 200 and the note posted. A paused project with a day-old stamp → `paused`, an
      enabled project with the same stamp → `stalled`.
- [ ] 4.4 Evidence: `go test ./internal/web -run 'Paused|MirrorRead' -v`.

## 5. Docs: what pause means (T5 — blocked by T1–T4)

- [ ] 5.1 `docs/fleet-console.md` (Projects/pause section, around `:450`): state that pause stops all forge
      traffic, that an accepted change request waits for resume, that the header says paused, and that a quota
      pause inherits the same freeze.
- [ ] 5.2 `docs/quickstart.md` §9 (`Pause, stop, back up`, around `:365`): the operator-visible version — what
      stops, what keeps running, what resume does.
- [ ] 5.3 Grep both docs for the old claim that a paused project still merges in-flight work and remove it.
- [ ] 5.4 Evidence: the grep in 5.3 returns nothing; `go test ./...` still green (docs do not change code).
