# Design — starting a rig whose daemon is down

## D1. A daemon that is not running is a fact, not a stale read

`read.RigStale` today means "the rig read failed", which covers a timeout, a broken JSON shape and a missing
daemon alike. The last of those is decidable: the CLI prints its own sentence, measured on this box —

```
Error: Daemon not running.
  This command needs a running daemon.
  Run 'rig up' (it auto-starts the daemon), or 'rig daemon start'.
```

`internal/rig` gains `ErrDaemonDown` (typed, `errors.Is`-able) and classifies a failed `rig` call whose stderr
carries that answer, or a refused local connection (`connection refused`, `ECONNREFUSED`), as that error. Everything
else keeps today's generic failure, which the lifecycle rule still refuses to act on.

`Rejected:` matching the sentence in `mirror`, `act` and `cmd` — three copies of a string is how the sha comparison
ended up in three packages. `Rejected:` treating any rig-read failure as "down" — a 20-second timeout on a busy box
would launch a second fleet over a live one.

## D2. Only the start half relaxes

When `RigDown` is set, `BuildLifecycleRigs` reports `SeatsKnown = true` and `Running = 0`, and `DecideLifecycle`
takes the same "a stopped rig is started before anything is written for it" branch it already has
(`internal/act/lifecycle.go:236`). The stop half is skipped entirely: `rig down` against a daemon that is not
running can only fail, and a fleet that does not exist cannot be quiet.

`Rejected:` a separate `RigDown` branch in the rule — one more path to test; the existing branch is exactly the
start-on-demand logic, and its precondition was the only wrong thing.

## D3. Demand must still be fresh

`startReason` already sums `Ready` only over managed projects whose forge read is fresh
(`internal/act/lifecycle.go:307-324`), and that stays. A down daemon plus a stale forge read starts nothing: an
empty item list from a broken read is not an empty board, and it is certainly not demand.

`Rejected:` starting a down rig on the mere presence of a project — that would boot a fleet on every reboot whether
or not anyone needs it, which is the opposite of what the lifecycle exists for.

## D4. One attempt per rig per backoff window

A start that fails (a spec that moved, a runtime that is not authenticated) must not retry every tick: at a 15 s
cadence that is 240 `rig up` calls an hour. The executor keeps `lastStart map[rigName]time.Time` in memory, the
same lifetime as the quiet streak, and refuses an attempt within `StartBackoff = 5 * time.Minute`. A process restart
forgets it, which can only delay a start by one window.

`Rejected:` persisting the attempt time — a schema change for a timer. `Rejected:` no backoff — measured cost, and
the failure is the operator's to fix, not the app's to hammer.

## D5. A cold launch gets its own timeout

`rig.DefaultTimeout` is 20 s (`internal/rig/exec.go:18`), sized for a read against a local daemon. A cold
`rig up <spec>` boots a daemon, the kernel and every seat; 20 s is not enough, and a timeout there is the worst
outcome — the fleet comes up while the app records a failure and (per D4) declines to look again for five minutes.

`Adapter.Up` therefore runs with `StartTimeout = 5 * time.Minute`, passed explicitly to that one call.

`Rejected:` raising `DefaultTimeout` — it would let a hung `rig ps` block the whole tick for five minutes.

## D6. One log line and one existing event, no new page

Success keeps the existing `rig.started` event with the reason it was started, now able to say
`daemon down; 3 ready item(s)`. A refused attempt (backoff) is a `note`-deduped log line, not an event and not a
notification: the operator asked for automation, and a page about a retry timer is noise. Failure keeps today's
`rig up failed` note.

`Rejected:` a Telegram page per start — the board and the audit surface already carry it, and `quiet-notify` owns
what deserves a page.

## Tests that prove the promises

| Promise | Test |
|---|---|
| The sentence is classified | fake runner returning the measured stderr → `errors.Is(err, rig.ErrDaemonDown)` |
| Other failures are not | timeout and malformed-JSON errors are not `ErrDaemonDown` |
| A down daemon starts a rig with demand | lifecycle input `RigDown`, `Running 0`, one managed project with `Ready 3` → one `LifecycleStart` with reason naming 3 items |
| No demand, no start | same input with `Ready 0` → no action |
| A stale forge read is not demand | `RigDown` + `Fresh false` → no action |
| The stop half stays dark | `RigDown` + quiet projects → no `LifecycleStop` |
| The backoff holds | two starts within 5 minutes → one `Up` call, one note; a third after the window → a second call |
| The launch has room | `Up` is called with a context deadline of at least `StartTimeout`, not the 20 s default |
