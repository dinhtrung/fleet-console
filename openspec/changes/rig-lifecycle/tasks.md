# Tasks — idle stop, demand start, slow poll

## 0. Ground rules

- Build gate for every task: `go build ./... && go vet ./... && go test ./...`, plus
  `openspec validate --all --strict` (baseline is 12 passed, 0 failed). Paste the real output into the PR body. No
  frontend target here — nothing in this change touches the board.
- **Fixed vocabulary. Do not invent a second name for any of these:**
  - config block `rig_lifecycle:` with `enabled` (bool), `idle_after` (duration), `idle_poll_max` (duration)
  - per-project `lifecycle: on|off`
  - audit kinds `rig.started`, `rig.stopped`
  - log prefixes `rig-lifecycle:` and `mirror: cadence`
- "Live row" means a queue row that is neither terminal nor blocked on the operator. `decide`'s
  `terminalRowStates` / `activeRowStates` are the vocabulary; a row blocked on `human@kernel` is neither.
- No Telegram message is sent by any task in this change. A test that asserts zero sends belongs in the tick test.
- Behaviour that is not in `specs/rig-lifecycle/spec.md` or `specs/poll-cadence/spec.md` is not yours to invent. If
  the decision needs a fact neither the plan nor the read carries, **stop and report** instead of adding a live
  read inside the tick.

## 1. Config and the demand signal (builder 1)

- [ ] 1.1 `internal/config/config.go`: add the `rig_lifecycle` block to the file shape and the effective settings
      with defaults `enabled: false`, `idle_after: 10m`, `idle_poll_max: 10m`; refuse `idle_poll_max <
      tick_interval` at load, naming both values and the key. File and default only — no env var, no flag.
      `--print-config` prints the three keys with their sources (`config.go` already renders `Sources`).
      Evidence: config tests for the defaults, for the block from a file, and for the refusal, quoted in the PR.
- [ ] 1.2 Per-project `lifecycle: on|off`: decode strictly (an unknown value is an error, not "on"), store on
      `config.Project`, and resolve the effective value in `--print-config`. A project without the key inherits
      `rig_lifecycle.enabled`.
      Evidence: two tests — a project that opts out and one that inherits — plus the printed line.
- [ ] 1.3 `internal/decide/decide.go`: add `Result.Ready int`, set to `len(ready)` **before** the concurrency limit
      is applied. Nothing else in `Decide` changes — the dispatch rules and every existing skip reason stay as they
      are, which is what `internal/decide/*_test.go` already pins.
      Evidence: one test with three ready items and one free seat asserting `Ready=3, Free=1, len(Dispatches)=1`;
      the whole existing suite green.

## 2. The pure decision (builder 1)

- [ ] 2.1 `internal/act/lifecycle.go`: `LifecycleInput` (per rig: bound projects, each with fresh-read, ready
      count, live rows; the running seat count; the projects' lifecycle setting; `idle_after`; the tick time and
      the previous quiet stamps) and `DecideLifecycle(in) []LifecycleAction` with actions `start` (rig, spec path)
      and `stop` (rig). Pure: no I/O, no clock, no globals, so a table test owns the whole matrix.
      Evidence: `go test ./internal/act -run Lifecycle` with the case names.
- [ ] 2.2 The table must cover, as separate cases: start on `Ready>0 && running==0`; no start when running>0; no
      start when the project opted out; no start when the read is stale; start on a claimable row with no ready
      item; stop after the window; no stop one tick before the window; no stop when a live row is addressed to a
      seat on the rig; no stop with a stale read; no stop with `enabled=false`; two projects sharing one rig, one
      quiet and one busy, producing no stop.
      Evidence: the case list and its pass output in the PR body.

## 3. Executing it in the tick (builder 1, after 1.3 and 2.1)

- [ ] 3.1 One small interface over the adapter (`Up(ctx, spec)`, `Down(ctx, name)`) plus the seat count the tick
      already reads, so `internal/act` stays testable without a real rig. Wire it in `cmd/fleet-console/main.go`
      where `rig.NewAdapter` is already constructed.
- [ ] 3.2 Start runs **before** any dispatch write for that rig. A failed `Up` skips each dispatch on that rig with
      `act-skip: <key>: rig <rig> up: <err>` and creates no worktree and no queue row. Put the guard at the top of
      the dispatch loop in `internal/act/actions.go` (or `tick.go` where the loop is driven), not inside `ExecGit`.
      Evidence: a fake rig that fails `Up` and a fake actions recorder asserting zero worktree and zero
      `queue create` calls.
- [ ] 3.3 Stop runs after the dispatch phase, so a tick that both merges the last item and finds the window elapsed
      stops on the same tick rather than the next. One log line
      `rig-lifecycle: <rig> stopped — quiet <duration>` and one `rig.stopped` audit event; `rig-lifecycle: <rig>
      started — <reason>` and `rig.started` for a start. A failure writes `rig-lifecycle: <rig> up failed: <err>`
      once per change (a `mirror.note`-shaped map), and no success audit event.
- [ ] 3.4 The tick test asserts zero `sendMessage` calls for both actions.
- [ ] 3.5 The spec path is `~/.openrig/specs/rigs/<rig>/rig.yaml`, the same convention `internal/preflight`
      already uses (`specDir`). A missing file is a start failure with that path in the message; never invent one.

## 4. The cadence (builder 2, blocked by 1.1)

- [ ] 4.1 `internal/mirror`: `Cadence{Base, Max}` with `Next(quiet bool) time.Duration` — double on quiet, cap at
      `Max`, reset to `Base` on busy — and a `Streak() int` for the log line. Pure arithmetic, no timers.
      Evidence: a table test for the doubling sequence, the cap, and the reset.
- [ ] 4.2 `mirror.Loop` takes the cadence instead of a bare interval and calls `onTick` for a verdict:
      `onTick func(TickResult) bool` returning busy. Keep the existing jitter, capped at the wait it is added to.
      `cmd/fleet-console/main.go` returns busy from the read it already has: an open item, an open change request, a
      non-terminal row, or a stale/failed read in **any** project.
      Evidence: a loop test with a stub `onTick` and an injected wait recorder asserting the sequence of waits and
      that the busy verdict resets it.
- [ ] 4.3 Log `mirror: cadence <wait> (quiet N)` on each change only, and
      `mirror: cadence <wait> reset: <the fact>` on a reset. No line when the wait does not change.
      Evidence: the log lines from a test, quoted.

## 5. Docs (builder 3, blocked by 3 and 4)

- [ ] 5.1 `PRODUCT.md` and `docs/my-stack.md`: what lifecycle does, the exact config block, the latency trade of
      `idle_poll_max`, the two log lines, and that a human-blocked row does not hold a rig open.
- [ ] 5.2 One operator paragraph: how to turn it off globally and how to keep one project warm
      (`lifecycle: off`), with the restart (`systemctl --user restart fleet-console`) that applies it.
- [ ] 5.3 Do not restate the requirements as prose; link `openspec/changes/rig-lifecycle/`. No screenshot.

## 6. Gate (whoever is last)

- [ ] 6.1 `go build ./... && go vet ./... && go test ./...` and `openspec validate --all --strict` in the PR body,
      real output, 12+ passed and 0 failed.
- [ ] 6.2 A hand proof on a spare port: build, run with `act: false` and a throwaway DB, and show `--print-config`
      rendering the block. Never point a probe at 8787.
