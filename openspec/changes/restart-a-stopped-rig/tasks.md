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
- [x] 4.2 `go build ./... && go vet ./... && go test ./...` → build ok, vet clean, **1208 passed in 23 packages**.
- [x] 4.3 `openspec validate --all --strict` → 34 passed, 0 failed.

## 5. The running count — the mechanism the live journal named

This section is the part of the plan the evidence actually caught, and the only part shipped in this change.

The proposal named two silent failure modes. The recorded window names a third, simpler one, and it explains both
halves of what the operator saw. `buildLifecycleRigs` counted **every seat row** as running. A stopped rig keeps its
rows, and those rows report a lifecycle that is not `running` (`exited`, `recoverable`, `attention_required` — the
`rig_seats` snapshot written while the fleet was idle has three `recoverable` rows for one project and four live-looking
rows for the other). So `Running` never reached 0: the start arm was never entered, and the stop arm fired on every
tick — which is exactly the `rig.stopped` / `rig-lifecycle: … stopped — quiet <dur>` line repeated every ~90 seconds
throughout the window, while three issues waited.

The accepted spec already says what the fix is: *"Up or down SHALL be read from the seats running on that rig on every
tick"*, and *"The operator stops the rig by hand: … the next tick reads zero seats, finds nothing to start, and records
no lifecycle action"*. The old count made that scenario false, not true.

- [x] 5.1 `internal/act/lifecycle.go`: `buildLifecycleRigs` counts a seat only when its lifecycle is `running`.
- [x] 5.2 `internal/rig`: the running state is named once (`rig.LifecycleRunning`, `(rig.Seat).Running()`), and
      `internal/mirror`'s builder filter uses it instead of its own string literal.
- [x] 5.3 `startReason`'s stopped arm reads `stopped_with_demand: <n> ready item(s), no running seat`, so a start line
      names the state it was read from; the daemon-down wording is unchanged.
- [x] 5.4 Tests: `TestBuildLifecycleRigsCountsOnlyRunningSeats` (table over `exited`, `recoverable`,
      `attention_required`, empty, mixed and all-exited rosters) and `TestStoppedRigStartsOnDemand` (demand → one start;
      no demand → none; stale read → none). Red/green proven by deleting the guard: `running = 1, want 0 for [exited]`.

Sections 1–3 stay open. They address silence — an unread roster, and a journal that never says why nothing happened —
and they are worth doing on their own; they are not what kept the rig down.
