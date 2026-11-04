# Proposal — silence when the board is empty

## Why

The progress notice is a projection of state, and one of the states it renders is "there is nothing to do":

```
RIG fleet-console · idle
  ⏸ dev-build-1  idle — nothing pending
  why: idle — no open work: every issue is closed
▶ in progress (0)   ⏳ pending (0)   ⏸ on hold (0)
```

That message carries no decision, no action and no change the operator did not already know. It is sent because a
trigger fired — a digest interval (`eff.NotifyInterval`) or a merge in the tick — not because anything needs him.
The consequence on the phone is a notification that costs attention and buys nothing, which is exactly how a real
alert gets trained into being ignored.

The board must keep showing the idle reason (`internal/progress/progress.go` `idleReason`, rule 5: "no open work:
every issue is closed"). This change is about the **notification**, not the board.

## What changes

- A **quiet state** is defined on the snapshot: no open epics, nothing in flight, nothing pending, nothing on hold,
  and no open decision. It is a pure predicate over the counts the snapshot already carries.
- When the snapshot is quiet, the notifier **sends nothing**, whichever trigger fired — digest interval or a merge
  in the applied tick. One log line records the decision (`notify: quiet — no open work; nothing sent`) so a
  reader of the log is never left guessing whether the send failed or was skipped on purpose.
- `--notify-report` stays **unconditional**: it is the operator asking explicitly, so a quiet board still answers.
  That is also the escape hatch for the one loss this change makes (see below).
- Escalations are untouched. A raised decision is by definition not quiet (open decisions count), so the decision
  message can never be muted by this rule.

## Losses, named

- The merge that closes the **last** open issue no longer produces a message. What it said ("merged #131, nothing
  left open") is now visible on the board, in the issue's own timeline, and via one `--notify-report`. If the
  operator ever wants that ping back, the escape hatch is the explicit report, not a config knob — a knob would
  reintroduce the noise this change exists to remove.
- A quiet project's digest stops entirely. A project that is deliberately parked and silent is the intended state.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `progress-notify`: adds the quiet-state requirement and the exception for the on-demand report; every other
  requirement (message content, dedupe, never-fail-a-tick, disabled-without-credentials) is unchanged.

## Impact

- `internal/progress/progress.go` — the predicate (or a field the renderer already computes) and its unit tests.
- `cmd/fleet-console/notify.go` — the early return in `sendProgress` plus the log line; `cmd/fleet-console/main.go`
  keeps its trigger conditions untouched (the gate moves to the notifier, where the dedupe already lives).
- `internal/progress/progress_test.go`, `cmd/fleet-console/notify_test.go` — quiet/non-quiet table tests against the
  fake Bot API the tests already use, counting `sendMessage` calls.
- `DESIGN.md` §5 (progress notices) and `PRODUCT.md` where the notice behaviour is described.
- No forge, store or config change. No new environment variable.
