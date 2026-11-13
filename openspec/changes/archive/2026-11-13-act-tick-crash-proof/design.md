# Design — act-tick crash proofing

## D1. The clock is normalised inside `RunTick`, not demanded from the caller

`RunTick` opens with `if in.Now == nil { in.Now = func() time.Time { return time.Now().UTC() } }` — the same
shape `housekeep.go:68-70` uses — before `Actions` is built. Every reader in the tick then sees a non-nil clock,
and `main`'s literal stays as it is.

*Rejected: make `main.go` pass `Now`.* It fixes today's crash and leaves the type nil-shaped, so the next caller
(the web answers path, a test harness, a future CLI verb) re-opens it. The package already treats an absent
dependency as "use the default"; the defect was one site ignoring that.

*Rejected: make `Now` a required constructor argument.* `TickInput` is a struct literal with fifteen optional
fields, five of which the test suite leaves empty on purpose; a constructor for one field is a second convention.

## D2. The silent-dispatch age reads `Actions.now`, so the raw field has no readers

`tick.go:318` becomes `age := dispatchAge(dispatch, actions.now)`. `dispatchAge` keeps its `func() time.Time`
parameter (it stays unit-testable with a fixed clock), but its only caller now hands it the guarded accessor.
`Actions.now` already returns UTC, so the age is unchanged for every tick that set a clock.

*Rejected: a nil check inside `dispatchAge`.* It would guard the symptom at the helper while leaving the raw
`in.Now` reachable from any new call site — the same class of bug one line away.

## D3. The recover lives in `Mirror.Loop`'s tick closure, once

The closure already owns "one tick, then decide the next wait" (`mirror.go:507-534`). It gains
`defer func() { if v := recover(); v != nil { … } }()` at its top; on a panic it logs
`tick: panicked: <value>` followed by the stack, sets `quiet, reason = false, "tick: panicked: <value>"` so the
next cadence is the base interval and the cadence line reads `reset: tick: panicked: …`, and returns. The loop
continues.

*Rejected: recover inside `act.RunTick`.* Two costs: unit tests would stop seeing the panic (a recover turns any
programming error into a returned error, and the test that should fail loudly would pass), and the recover would
also hide panics in the store and forge adapters the tick calls, which is a wider claim than the one this change
makes. The goroutine that dies is the one that must contain the death.

*Rejected: recover in `main`'s per-project act block.* It is the smaller diff, but it leaves `mirror.Tick`,
`BuildSnapshot`, `decide.PlanTick` and `sources()` able to kill the process, and the next panic report is then
about a different closure. One loop, one recover.

*Rejected: rely on the unit's `Restart=on-failure`.* It works on homebox and does nothing on a box where the
binary is started by `mise` — which is where this crashed. A restart loop at every tick is also strictly worse
than one bad tick.

## D4. The panic vocabulary: one prefix, the value, the stack, nothing else

Exact line: `tick: panicked: <value>`, then the `debug.Stack()` text through the same `m.logf`. `<value>` is
`%v` of whatever was recovered. No error wrapping, no new error type, no audit row, no decision raise, no
counter.

*Rejected: raise an operator question per panic.* `RaiseOperatorQuestion` dedupes per `(item, head)`; a panic has
neither, so the dedupe key would have to be invented, and a permanent panic would page once per tick. The
journal is the surface for "the loop is limping".

*Rejected: log only the value without the stack.* A recovered panic without a stack is unactionable; it is the
one case where the stack is the whole report.

## D5. The regression tests sit at the seam, not at the helper

Two new tests, each pinning the line that was missing:

1. `internal/act`: `TickInput` built with no `Now` and a `decide.IntentSilentDispatch` intent over a live row
   with a parseable `CreatedAt` — assert the raise happens and the test does not panic. (`dispatchAge(row, nil)`
   is *not* the test; it pins the helper, and the helper was never the defect.)
2. `internal/mirror`: a `sources` (or `onTick`) that panics on the first call and not on the second — assert the
   second tick runs, the log carries `tick: panicked:`, and `Loop` returns only when the context is cancelled.

*Rejected: a build-tag or `go vet` check for "TickInput without Now".* The seam test is three lines and fails on
the real crash; the vet rule would fail on the twelve test files that omit other optional fields.

## D6. Documentation says what the line means, not how the code works

`docs/fleet-console.md` gains a short subsection: `tick: panicked:` is a contained failure — the process is alive,
the loop is at the base cadence, and the stack names the site; and the console must run under a supervisor
(systemd user unit on a box it owns, `Restart=on-failure`), because a contained panic still means the tick did
nothing.

*Rejected: no docs.* The new line is the only thing an operator has; an unexplained prefix in a journal is what
this change exists to avoid.
