# Proposal — a tick that panics is contained, and a tick without a clock is not a nil call

## Why

**1. The daemon died on a nil function call, not on bad data.** On 2026-11-13 at 10:42:42 the console on box
`NTTD-7H14Z64` (launched by `mise`, syslog tag `mise[12369]`, no supervisor) printed a SIGSEGV and exited:

```
act.dispatchAge(...)   internal/act/silent_dispatch.go:174
act.RunTick(...)       internal/act/tick.go:318
main.run.func5(...)    cmd/fleet-console/main.go:853
mirror.(*Mirror).Loop  internal/mirror/mirror.go:580
```

`tick.go:318` is `age := dispatchAge(dispatch, in.Now)`, and `dispatchAge` ends in `return now().Sub(created)`
(`silent_dispatch.go:169-175`). `main` builds `act.TickInput` at `main.go:857-866` and never sets `Now`, so
`in.Now` is a nil `func() time.Time` and `now()` is a nil dereference. Reproduced in the tree with a three-line
test: `panic: runtime error: invalid memory address or nil pointer dereference` at `silent_dispatch.go:174`, the
same pc offset as the journal.

**2. The package already knows the clock is optional; one call site forgot.** `Actions.now()`
(`internal/act/actions.go:50-55`) returns `time.Now().UTC()` when `a.Now == nil`, and `housekeep.go:68-70`
normalises the same nil by hand. `tick.go:318` bypasses both and calls the raw field. So the defect is not a
missing guard in the type — it is one call site that did not use the guard the type already has.

**3. CI cannot see it, because only production omits the clock.** Every act test injects one
(`internal/act/tick_test.go:99,235,307,365,390,438,473,548,629`, and the same in nine other files); `main` does
not. The untested line is exactly the wiring, which is why the binary that shipped a passing suite crashed on
the first silent-dispatch intent it planned.

**4. Nothing in the tree recovers a panic.** `grep -rn 'recover()' --include=*.go .` returns nothing: a panic in
the mirror goroutine is the whole process. On homebox `Restart=on-failure`/`RestartSec=2` turns that into a crash
loop at every tick; on the box that actually crashed the process was launched by `mise` and **stayed dead** —
the control plane was dark and nothing said so.

**5. The trigger is rare enough to hide.** `tick.go:292-318` runs the age read only for
`decide.IntentSilentDispatch` — a live dispatch that produced no change request after `SilentDispatchAfter` with
a parseable `CreatedAt`. A week of ticks can pass without one.

## What changes

- **`TickInput.Now` becomes optional in fact, not only in the comment.** `RunTick` normalises nil to
  `time.Now` before anything reads it, and the silent-dispatch age reads `Actions.now` instead of the raw field,
  so no future call site can re-open the nil.
- **One recover, at the goroutine that owns the loop.** `Mirror.Loop`'s tick closure recovers, logs
  `tick: panicked: <value>` plus the stack, reports the tick as busy, and runs the next one. The process
  survives a bad tick; the loop stays honest about why it slowed down.
- **A regression test at the production seam.** `TickInput` built the way `main` builds it — no `Now` — with a
  silent-dispatch intent, asserting a report and no panic. That is the line the suite was missing.
- **A loop test that a panic does not end the loop.** Sources that panic once, a second tick that must happen,
  and the exact log line.
- **One paragraph of operator documentation** for the new line and for the fact that the console must be
  supervised on every box it runs on.

## Losses, named

- **A permanently broken tick is now a log line every tick instead of a crash.** That is the point, and it is
  also a risk: an operator who never reads the journal gets a silently slow loop instead of a loud restart. The
  line is `tick: panicked:` with a full stack, and the cadence log says `reset:` on the same tick, so the two
  reads are adjacent.
- **One more recover in the codebase.** `recover()` is easy to copy; the design fixes it to one place and names
  the reason it may not be copied into `RunTick`.
- **No audit row and no phone message per panic.** A recurring panic must not page on a loop. The journal is the
  surface; this change buys survival, not notification.
- **The lazy clock stays lazy.** `TickInput.Now` remains optional rather than becoming required, so a test that
  forgets it silently reads the wall clock instead of failing to compile.

## Capabilities

- **New: `act-tick`** — the tick's contract with the process: no nil clock, one contained panic, one honest line.

## Impact

`internal/act/tick.go`, `internal/act/silent_dispatch.go`, `internal/act/tick_test.go`,
`internal/mirror/mirror.go`, `internal/mirror/mirror_test.go` (or `loop_panic_test.go`), `docs/fleet-console.md`.
No schema change, no config key, no new dependency.
