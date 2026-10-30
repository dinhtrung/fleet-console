# Tasks — progress on the phone

A box is ticked only with the command that proved it, quoted in the pull request.

## 1. The projection (issue: reporting)

- [ ] 1.1 `internal/progress`: `Snapshot`, `ProjectBlock`, `RigBlock`, `SeatLine`, `Item`, `Counts` and `Build(read, rows, project, eff)`. Every project field the service reads is copied, not summarised; every seat in `read.Seats` becomes a `SeatLine`. Verify: `go test ./internal/progress -run TestBuildProjectBlock` asserts each field from a fixture row, and `TestBuildSeats` asserts one line per roster seat.
- [ ] 1.2 `Render(s, now)`: the contract in `design.md` §2 — project block, rig block (header `busy N/M` or `idle`, then one line per seat), four buckets, empty buckets printed, 5-entry cap with `+N more`, 3900-character hard cap that never truncates the project block or the rig header. Verify: golden file plus a table test for the caps and the length bound.
- [ ] 1.3 Repo URL rule: `repo_url` verbatim, GitHub derived, GitLab derived plus a config warning. Verify: table test with the three cases, the warning asserted on the config side.
- [ ] 1.4 Roster in the message: seats and `busy N/M` when the tick had a roster, `roster unavailable: <why>` when it did not, and never an error out of the renderer. Verify: two cases in one test, `Send` asserted in both.
- [ ] 1.5 Idle reasons: the rig-level rule chain (`design.md` §2a, rules 1–8) and the per-seat rules (lifecycle, reviewer, builder with nothing pending, builder waiting for a dispatch). Each line is the exact sentence in the design, and rule 8 quotes the plan's skip text instead of inventing one. Verify: one fixture per rule in a table test asserting the exact string, plus a case proving `no free builder` never produces an `idle` report.

## 2. Delivery and dedupe (issue: reporting)

- [ ] 2.1 `internal/alert`: a `Notifier` that renders, hashes the body minus the timestamp, and sends only when the hash is new; `progress.sent` stores `<hash>:<messageID>`. Verify: two identical renders → one `Send`; a changed bucket → a second `Send`.
- [ ] 2.2 Failure path: 15s timeout, one retry after 2s, then stderr `alert: progress send: …` plus a `progress.failed` event, and the caller's tick still returns nil. Verify: fake transport returning an error, tick result asserted nil.
- [ ] 2.3 Boot behaviour: one line when the token or chat id is missing, and every trigger becomes a no-op. Verify: boot log assertion in `cmd/fleet-console` (empty env) and zero sends through the fake.

## 3. Triggers (issue: wiring)

- [ ] 3.1 After-merge trigger in the applied tick: build the snapshot **after** the merge and send once per `MergeResult`. Verify: act test with a fake sender asserts one message per merge and none without a merge.
- [ ] 3.2 `--notify-report`: no mirror, read store and roster, render, send; exit 0 delivered, 1 transport failure, 3 not configured. Verify: three table cases against a fake Bot API server.
- [ ] 3.3 Wire the existing `Escalator` to the same client so decision escalations actually reach the phone (today nothing calls it). Verify: a decision raised in a tick produces one `telegram.sent` event and one send.
- [ ] 3.4 `notify_interval` (default 0 = off): a changed-hash digest at most that often. Verify: two ticks inside the interval send once; a state change inside the interval still sends; an idle rig whose reason did not change sends nothing.
- [ ] 3.5 Docs: `docs/cutover.md` Telegram section covers both message kinds; the env vars are named in `docs/quickstart.md` §3 with the "unset means off" line.

## 4. Gate

- [ ] 4.1 `go build ./... && go vet ./... && go test ./...`, then `openspec validate --all --strict`. Verify: real output quoted in the pull request.
