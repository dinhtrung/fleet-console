# Tasks — a stopped rig is not a quiet rig

## 1. The start half survives an unread roster

- [ ] 1.1 `internal/act/lifecycle.go`: keep `lastRunning map[string]int` on the `Lifecycle` coordinator, updated only when `SeatsKnown` is true.
- [ ] 1.2 `buildLifecycleRigs`: when `SeatsKnown` is false and `lastRunning[rig] == 0`, build the entry as stopped (`Running: 0`) with a flag that the roster is unknown, instead of dropping the rig from the decision.
- [ ] 1.3 `startReason`: the unknown-roster arm returns `roster unknown; N ready item(s)`; the known-stopped arm keeps today's wording.
- [ ] 1.4 `DecideLifecycle`: the stop half still refuses an unknown roster — only the start half gains the arm.
- [ ] 1.5 Tests: stale + last 0 + ready → one start; stale + last >0 → none; stale + no demand → none; known-stopped + demand → unchanged.

## 2. Every rig decision is one line

- [ ] 2.1 `cmd/fleet-console/main.go` (or the lifecycle executor): one log line per rig per tick — `rig-lifecycle: <rig> <start|stop|none> — running=<n> ready=<n> seats_known=<bool>` plus the reason for start/stop.
- [ ] 2.2 The line is built from the same `LifecycleRig` the rule decided on; no second computation.
- [ ] 2.3 Test: a table over {running quiet, stopped with demand, stopped no demand, unknown roster} asserts the line text.

## 3. A plan skip reaches the journal

- [ ] 3.1 `internal/act/tick.go`: print one `act-skip:` line per (item, reason, head) from `decide.Result.Skips`, reusing the ledger key that already rate-limits notices.
- [ ] 3.2 No repeat while the reason and head are unchanged; a new head or a new reason prints again.
- [ ] 3.3 Test: two ticks with `no_free_builder` → one line; reason changes → second line.

## 4. Docs and gate

- [ ] 4.1 `docs/fleet-console.md`: how to read `rig-lifecycle:` lines and what `roster unknown` means; when an operator should run `rig start` by hand.
- [ ] 4.2 `go build ./... && go vet ./... && go test ./...` (paste output; the two known environment failures may be named, not hidden).
- [ ] 4.3 `openspec validate --all --strict`.
