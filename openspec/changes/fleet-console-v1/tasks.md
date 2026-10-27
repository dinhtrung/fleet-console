# Tasks — fleet-console v1

Each phase ships on its own. A box is ticked only with the command that proved it.

## 1. Skeleton and registry (P0)

- [ ] 1.1 Go module, `cmd/fleet-console`, `internal/{store,forge,rig,git,decide,act,web}`, SQLite via `modernc.org/sqlite` (no cgo), schema from design §5 with migrations. Verify: `go build ./...` and `go test ./internal/store` creates and re-opens a temp DB.
- [ ] 1.2 Registry CRUD + seed for `app` (repo `example-app`, rig `fleet-omp`, builders `build-1/2`, reviewer `review`). Verify: `fleet-console projects add …` then `GET /api/projects` returns it.
- [ ] 1.3 Config file (`/etc/fleet-console/config.yaml` or `~/.config/fleet-console/`) for tokens, ports, alert routes; tokens from the environment only, redacted from logs. Verify: a startup log with a fake token prints `REDACTED`.
- [ ] 1.4 systemd user unit + `--healthcheck` subcommand. Verify: `systemctl --user start fleet-console` then `curl -fsS 127.0.0.1:8787/api/health`.

## 2. Mirror (P0)

- [ ] 2.1 Port the work-item reader: GraphQL paged 100/page, ≤10 pages, iid as int, `related_items`, parent, labels, state. Verify: a fixture test of one real page parses 100 items with no null iid.
- [ ] 2.2 Read merge requests + pipelines (head pipeline, job list, `allow_failure`, `detailed_merge_status`). Verify: `TestPipelineFacts` over recorded payloads for green/red/flaky/docs-only.
- [ ] 2.3 Read rig: `rig queue list -A -a --json` and `rig ps --nodes --json` (bare array), map to `queue_row` and seat records. Verify: a test over this box's real output.
- [ ] 2.4 Read origin branches per repo and record `feature/<iid>-*`. Verify: `TestBranchIndex` with a recorded `ls-remote`.
- [ ] 2.5 Mirror loop with jitter, per-project stamps, and a `stale` flag surfaced to the API. Verify: kill the forge CLI and confirm the board shows the last sync time instead of an empty board.

## 3. Decide (P1, dry-run)

- [ ] 3.1 Candidate filter and blocking rules exactly as design §6, as pure functions over a mirror snapshot. Verify: table tests for epic, onhold parent, closed parent, live row body, branch on origin, smaller related iid.
- [ ] 3.2 Housekeeping passes: prune, reset stalled (≤3 attempts), marker reconcile, re-nudge unclaimed (15 min/30 min cooldown), epic close. Verify: one test per pass, each asserting the event rows written.
- [ ] 3.3 Parked / marker-leak / red-gate detectors raising `decision` rows. Verify: replay the two real incidents as fixtures (#42 parked with a green MR and no verdict; a band flake on a head pipeline) and assert the decision kind and question text.
- [ ] 3.4 Golden test against the live loop: run the console's plan and `dispatcher.py --apply=false` for the same project on the same mirror, assert identical task sets and skip reasons. Verify: the test passes for ten consecutive ticks in a week of shadow running.

## 4. Act (P2)

- [ ] 4.1 Dispatch intent: fetch, base sha, worktree add-or-reuse (reuse when HEAD is the branch, skip when other branch + dirty), brief from template, `rig queue create --source human@kernel`, set `status::ongoing`, record dispatch + lease. Verify: a real task dispatched with the old timer stopped; worktree and row exist; a second run of the intent is a no-op.
- [ ] 4.2 Row actions: resolve (decision text), handoff, close-as-superseded, re-nudge. Verify: each writes an event with the row's before/after state and the CLI exit code.
- [ ] 4.3 Merge watchdog: four facts re-read in the act pass; refuse with the missing fact named. Verify: four refusal tests (no verdict / sha drift / red pipeline / not mergeable) and one end-to-end merge on a docs-only MR.
- [ ] 4.4 Post-merge: verify the task closed, complete it when the body only `Refs` it, clear `status::ongoing`, unblock the next task. Verify: a test where the MR body says `Refs #x` and the task ends closed with an event naming the reason.

## 5. Web board (P2)

- [ ] 5.1 Static build embedded (`go:embed`), served at `/`, SSE at `/api/stream`. Verify: `go build` then the board renders from the embedded assets with no node at runtime.
- [ ] 5.2 Board screen with the four states and the reason column; project switcher; counts. Verify: a screenshot with two projects, one paused, one with a parked decision.
- [ ] 5.3 Decisions screen: oldest first, SLA badge, options, free-text answer, optimistic update confirmed by SSE. Verify: answering a fixture decision moves it out of the queue and writes one event.
- [ ] 5.4 Seat screen: state, observed model, permission mode, last activity, pane tail (read-only). Verify: shows the real `fleet-omp` seats, including a seat parked on a permission prompt.
- [ ] 5.5 Task drawer: brief, branch, worktree, MR + job list, verdict, event trail, issue/MR links. Verify: opens for a task that was dispatched, reviewed and merged.

## 6. Alerts and multi-project (P3)

- [ ] 6.1 Telegram escalation on SLA breach and on `quota` decisions; one message per decision, edited rather than repeated. Verify: a forced SLA breach delivers one message containing project, task and board deep link.
- [ ] 6.2 Second project onboarded (a real second repo), with isolated capacity. Verify: both projects dispatch on the same box without an iid collision or a shared worktree path.
- [ ] 6.3 Quota handling: ≥2 seats stalled on one account within 5 min raises one `quota` decision and pauses dispatch to that rig. Verify: simulated stall in a test, and one real observation on the box.

## 7. Cutover (P3)

- [ ] 7.1 Import `~/.local/state/dispatcher.json` (`dispatched`, `attempts`, `nudges`, `closers`) so nothing re-dispatches. Verify: after import, a dry-run plan for `app` is a subset of the old dispatcher's.
- [ ] 7.2 Disable `dispatcher.timer`, enable the console's act loop, watch one full task end-to-end. Verify: task dispatched, reviewed, merged, epic closed, board empty with a reason per closed task.
- [ ] 7.3 Update `~/my-stack.md` §3.8/§3.9 to describe the console as the dispatcher (one doc the owner reads after a reboot), and keep the old script in `~/bin` as the fallback with a note. Verify: the section names the units, the port, the registry file and the kill switch.
