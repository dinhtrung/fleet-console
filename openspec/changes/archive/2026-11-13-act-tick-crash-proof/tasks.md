# Tasks — act-tick crash proofing

A task is done only with the command that proved it, quoted in the pull request body. Epic: GitHub
`dinhtrung/fleet-console#<epic>`. Code tasks are children T1 and T2; T3 documents them.

## 0. Ground rules — fixed vocabulary

**The clock.** `act.TickInput.Now` is *optional*: a nil means "read the wall clock". Normalised in exactly one
place, the first statement of `RunTick`:
`if in.Now == nil { in.Now = func() time.Time { return time.Now().UTC() } }`. `Actions.now()`
(`internal/act/actions.go:50-55`) stays the only accessor code calls; the raw field is read only by `RunTick`'s
normalisation. No other package may read `TickInput.Now`.

**The panic line.** Exactly one prefix, produced exactly once per panic, at `internal/mirror/mirror.go` in the
`Loop` tick closure:

```
tick: panicked: <value>
<debug.Stack() text>
```

`<value>` is `%v` of `recover()`'s result. The same value is the cadence reason, so the next cadence line reads
`cadence <wait> reset: tick: panicked: <value>`. No error type, no audit kind, no queue row, no phone message, no
counter — a panic is a journal fact, not a work item.

**What "the loop survived" means.** `Loop` returns only on `ctx.Done()`. A recovered panic sets
`quiet, reason = false, "tick: panicked: <value>"` and returns from that one tick; the timer/wake select is
entered exactly as after any other failed tick.

**Where the recover may live.** One `recover()` in the whole tree, at the top of the `tick := func()` closure in
`Mirror.Loop`. Not in `act.RunTick`, not in `main`'s per-project act block, not in an adapter. If a second one
seems necessary, stop and report instead of adding it.

**Stop and report instead of inventing.** Two things this change does not decide and a builder must not decide on
its own: (a) any behaviour change to `decide` or the merge gate; (b) any new config key. If the fix appears to
need either, stop with the finding in the pull request.

## 1. T1 — the act tick has no nil clock

- [x] `internal/act/tick.go`: normalise `in.Now` at the top of `RunTick`, before `Actions` is built, with the
      comment that names the crash (`#<epic>`, `silent_dispatch.go:174`).
- [x] `internal/act/tick.go:318`: `age := dispatchAge(dispatch, actions.now)` — the guarded accessor, never the
      raw field. Confirm no other reader of `in.Now` exists: `grep -n "in\.Now" internal/act/*.go` answers one
      line in `lifecycle.go` (a `time.Time`, not a func) plus `housekeep.go`'s own normalisation.
- [x] `internal/act/silent_dispatch_test.go` (or `tick_test.go`): `TestTickWithoutAClockStillRaisesTheStall` —
      `TickInput` with `Now` unset, one `decide.IntentSilentDispatch` intent, a live dispatch row whose
      `CreatedAt` parses and is older than the threshold; assert `report.Decisions` has the row and no panic.
- [x] Keep `dispatchAge`'s signature (`func(store.DispatchRow, func() time.Time) time.Duration`) and its existing
      zero-time behaviour: an unparseable `CreatedAt` still answers 0, and the tick still skips with
      "the dispatch's age cannot be read".

Evidence: `go test ./internal/act/ -run 'TestTickWithoutAClock|Silent' -count=1 -v` green, and the same test red
when the normalisation line is removed (quote both runs in the pull request body).

## 2. T2 — a panicking tick does not end the loop

- [x] `internal/mirror/mirror.go`: add the `defer`/`recover` at the top of the `tick := func()` closure in `Loop`,
      logging `tick: panicked: %v` then `debug.Stack()` through `m.logf`, and setting the busy cadence reason.
      Do not swallow the panic anywhere else.
- [x] `internal/mirror/loop_panic_test.go`: `TestLoopSurvivesAPanickingTick` — `sources` (or `onTick`) panics on
      the first call and returns normally on the second; the loop must tick a second time, log a line containing
      `tick: panicked:`, and return only when the context is cancelled. Model the harness on
      `internal/mirror/loop_cadence_test.go:127` (`TestLoopCadenceTreatsAFailedReadAsBusy`).
- [x] The cadence after a panic is the base interval: assert the line `reset: tick: panicked:` follows the panic
      line in the captured log.

Evidence: `go test ./internal/mirror/ -run 'TestLoopSurvivesAPanickingTick|TestLoopCadence' -count=1 -v` green.

## 3. T3 — the operator can read the line

- [x] `docs/fleet-console.md`: a subsection naming `tick: panicked:` — what it means (the tick failed, the process
      is alive, the loop is at the base cadence), how to read the stack, and that the console must run under a
      supervisor (`Restart=on-failure` user unit); a box that starts it from a task runner with no restart leaves
      the control plane dark after any panic.
- [x] No new file, no new section in `docs/design/`.

Evidence: the doc renders in the pull request diff and `grep -n 'tick: panicked:' docs/fleet-console.md` returns
the subsection.

## 4. Verification, before the pull request leaves draft

- [x] `go test ./... -count=1` green.
- [x] `CGO_ENABLED=0 go build -trimpath -tags embedboard -o /tmp/fleet-console-check ./cmd/fleet-console` green.
- [x] `openspec validate act-tick-crash-proof --type change --strict` and `openspec validate --all --strict | tail -3`
      green, with the totals line quoted in the pull request.
- [x] The pull request body carries: the issue number it closes, the two red/green runs from T1, the mirror test
      name from T2, and the totals line from the change-set validation.
