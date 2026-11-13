# Tasks — a rig that is down is started when work arrives

A box is ticked only with the command that proved it, quoted in the pull request.

## 0. Ground rules

**Vocabulary:** `rig.ErrDaemonDown` (typed, `errors.Is`-able), `rig.StartTimeout = 5 * time.Minute`,
`TickResult.RigDown bool`, `mirror status rig_down`, `act.StartBackoff = 5 * time.Minute`,
`act.LifecycleRig.RigDown bool`, event `rig.started` (existing), log prefix `rig-lifecycle:` (existing).

**Classification rule**, in one place (`internal/rig`): a failed rig call is `ErrDaemonDown` when its stderr carries
the daemon's own sentence (`Daemon not running`, `needs a running daemon`) or a refused local connection
(`connection refused`, `ECONNREFUSED`). Nothing else — a timeout, a bad JSON shape or an unknown error stays a
generic failure.

**Start reason strings:** `daemon down; <n> ready item(s)` when demand triggered the start, `<n> ready item(s) and no
running seat` (existing wording) when the daemon is up.

**No new config key** and no schema change: the backoff is a constant, the flag rides the existing tick result.

**Build gate:** `go build ./... && go vet ./... && go test ./...`; `openspec validate --all --strict`.

## 1. Detect and carry the fact (one issue: `internal/rig`, `internal/mirror`, `internal/store`)

- [ ] 1.1 `ErrDaemonDown` + `isDaemonDown(stderr string) bool` in `internal/rig`, wrapped by the adapter's error path
      so every verb that talks to the daemon inherits it. Verify: `go test ./internal/rig -run TestErrDaemonDown` —
      the measured sentence, the refused connection, a timeout (false), malformed JSON (false).
- [ ] 1.2 `RigDown` on `mirror.TickResult` — set when the rig read error is `ErrDaemonDown`, false alongside
      `RigStale` otherwise — and carried into the stored mirror status row next to `rig_stale`. Verify:
      `go test ./internal/mirror -run TestRigDown`, and a store round-trip asserting the column reads back.
- [ ] 1.3 Nothing else changes in the busy logic: a down daemon already reads as busy (`internal/mirror/busy.go:52`),
      which keeps the poll at the base interval. Verify: a test pins that (`Busy` true with reason naming the rig
      read failure) so a later refactor cannot quietly slow the reaction down.

## 2. The rule and the backoff (one issue: `internal/act`)

- [ ] 2.1 `LifecycleRig.RigDown`, set by `BuildLifecycleRigs` from the tick fact, and `DecideLifecycle` treating it as
      `SeatsKnown = true, Running = 0` **for the start branch only**. Verify:
      `go test ./internal/act -run 'TestDecideLifecycleDaemonDown'` — start with demand, no start without demand, no
      start on a stale read, no stop while down.
- [ ] 2.2 `startReason` names the cause: `daemon down; N ready item(s)` when the rig is down, the existing wording
      otherwise. Verify: the two strings asserted in the same table.
- [ ] 2.3 `LifecycleExecutor.Start` keeps `lastStart map[string]time.Time` and refuses an attempt inside
      `StartBackoff`, logging one deduplicated line via `note()`. A successful start clears the entry's effect by
      recording the attempt time like a failure does. Verify: three ticks inside the window → one `Up` call; a fourth
      after the window → a second call (fake clock).
- [ ] 2.4 The success path is unchanged: `rig-lifecycle: <rig> started — <reason>` and one `rig.started` event whose
      detail is the reason.

## 3. The launch budget, the wiring and the docs (one issue: `internal/rig`, `cmd/`, `docs/`)

- [ ] 3.1 `Adapter.Up` runs with `StartTimeout` (5 minutes) explicitly, not `DefaultTimeout`. Verify:
      `go test ./internal/rig -run TestUpTimeout` asserting the deadline handed to the runner, and that the other
      verbs still use 20 s.
- [ ] 3.2 `cmd/fleet-console/main.go:818` passes the new fact into `lifecycle.Decide` (a `RigDown` argument or a
      field on the facts), with no other behaviour change in the tick. Verify: `go build ./...` plus the existing
      loop tests; the wiring is one line and must not reorder starts and stops.
- [ ] 3.3 `docs/fleet-console.md`: one paragraph in the lifecycle section — a daemon that is not running means every
      rig is down, so demand starts it; the backoff and the cold-start timeout are named; the stop half stays dark.

## 4. Gate

- [ ] 4.1 `go build ./... && go vet ./... && go test ./...` — full output in the PR.
- [ ] 4.2 `openspec validate daemon-down-start --type change --strict` and `openspec validate --all --strict`,
      totals quoted.
- [ ] 4.3 On homebox, with `act: true`: `rig daemon stop`, then place one ready item on a managed project, and read
      the journal — one `rig-lifecycle: tsim-fleet-claude started — daemon down; 1 ready item(s)`, one `rig.started`
      event, and seats booting. Restore the daemon if the test is run on a box that must stay up.
