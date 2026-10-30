# Cutover — mirror and decide dry-run

The mirror is read-only and decide is pure. `fleet-console --tick` runs one pass — forge items, links, change
requests and checks, the rig queue and seats, the origin branch index — writes only the mirror/cache tables, and
prints the decide plan. It performs no forge, rig or git write. The server runs the same pass on a 15 s interval
with 0–5 s of jitter; until the act loop lands, its intents are logged and not executed.

```sh
# one manual tick (dry-run; caches only)
./fleet-console --tick --db fleet-console.db

# the live dispatcher's plan for the same repository, for comparison
python3 ~/bin/gh-autodispatch.py          # dry-run; --apply would write
```

## First tick — 2026-10-28T18:17:25Z

Project `fleet-console` (GitHub `dinhtrung/fleet-console`, rig `fleet-console`, cap 2):

```json
{
  "status": "nothing",
  "free": 1,
  "skips": [
    {"number": 1,  "reason": "epic"},
    {"number": 6,  "reason": "live_row", "detail": "qitem-20261028181302-bf96d434b4ec0462"},
    {"number": 7,  "reason": "blocked",  "detail": "#6"},
    {"number": 10, "reason": "blocked",  "detail": "#7"}
  ],
  "intents": [],
  "events": []
}
```

The live dispatcher at the same moment:

```
#10 skip: blocked by [7]
#7 skip: blocked by [6]
open epics [1]; open tasks [6]
free builders: ['dev-build-2@fleet-console']
nothing: no task is ready
```

Both plans are empty and both see one free builder: **0 dispatch diffs**. The only difference is vocabulary — the
new plan names `#6` as `live_row` (it is the issue this seat is working), the dispatcher lists `#6` as an open task
but does not dispatch it. No item differs in the dispatch set.

## Ten consecutive ticks

The comparison is per tick against the live dispatcher, not against the previous tick. The world changed once
during the run: issues `#19`–`#24` were opened at ~18:21Z.

| tick | at (UTC) | status | free | dispatches |
|---|---|---|---|---|
| 1 | 2026-10-28T18:17:25Z | nothing | 1 | — |
| 2 | 2026-10-28T18:18:01Z | nothing | 1 | — |
| 3 | 2026-10-28T18:19:12Z | nothing | 1 | — |
| 4 | 2026-10-28T18:19:37Z | nothing | 1 | — |
| 5 | 2026-10-28T18:20:03Z | nothing | 1 | — |
| 6 | 2026-10-28T18:20:30Z | nothing | 1 | — |
| 7 | 2026-10-28T18:20:56Z | nothing | 1 | — |
| 8 | 2026-10-28T18:21:23Z | nothing | 1 | — |
| 9 | 2026-10-28T18:21:50Z | planned | 1 | #19 → dev-build-2@fleet-console |
| 10 | 2026-10-28T18:22:17Z | planned | 1 | #19 → dev-build-2@fleet-console |

After `#19` appeared, the live dispatcher planned the same action:

```
open epics [1]; open tasks [6, 19, 20, 21, 22, 23, 24]
free builders: ['dev-build-2@fleet-console']
#19 would dispatch to dev-build-2@fleet-console (dry-run)
plan: 1 dispatch(es) (dry-run)
```

**Diff count: 0 for all ten ticks** (dispatch sets and free-builder counts equal). Ticks 1–8: both empty. Ticks
9–10: both dispatch `#19` to `dev-build-2`.

`gh-autodispatch.service` is currently `inactive`; these dry-runs are the comparison baseline. During the week,
enable the live dispatcher's dry-run (or keep comparing its timer's plan) and add one row per day; cutover needs
ten consecutive ticks with zero diffs.

## What a tick proves beyond the dispatch set

- **Stale never reads as empty.** `TestMirrorStaleKeepsCachedRowsAndStamp` fails the forge read after a good tick and
  asserts the cached item rows and the last `last_forge_at` survive while the status block turns stale;
  `TestBoardReportsStaleWithLastSyncAndCachedItems` asserts `GET /api/board` returns `stale: true`, the previous
  `synced_at`, and the cached items.
- **Housekeeping runs before the early return.** One test per pass:
  `TestHousekeepingPrune`, `TestHousekeepingResetStalled`, `TestHousekeepingAbandonsAtMaxAttempts`,
  `TestHousekeepingReconcileMarker`, `TestHousekeepingRenudge`, `TestHousekeepingRenudgeHonoursCooldown`,
  `TestHousekeepingCloseEpic`; each asserts the events the pass would write.
- **Incidents name the question.**
  - `TestIncidentParked`: `test #5: Connection pool has an open change request with no verdict and no live row.
    Accept it, reject it, or reopen the seat?`
  - `TestIncidentMarkerLeak`: `test #5: change request #20 merged but the item is still open. Close it now?`
- **Branch index.** `TestBranchIndex` over a recorded `git ls-remote --heads origin` listing.

## Not yet part of this comparison

The act loop is a separate change: this dry-run writes no marker, comment, close or merge, creates no queue row and
does not touch a worktree. A week of ticks with the same zero-diff result is the precondition for enabling it.

## P4 cutover (issue #10)

### Dispatcher state import

```
$ fleet-console --import-dispatch-state ~/.local/state/gh-autodispatch.json \
    --project-key fleet-console --db cutover.db
imported 1 dispatch row(s) into fleet-console

sqlite> select item_number,attempt,seat,branch,qitem_id,base_sha,worktree from dispatch;
10|1|dev-build-1@fleet-console|feature/10-p4-alerts-quota-second-project|qitem-20261028190805-ec177bebfc5be46e|79acffb1643a19fdc6d6d8bdc3f4c10ca2bc13a8|/home/dinhtrung/develop/wt-fc-10

$ fleet-console --import-dispatch-state ~/.local/state/gh-autodispatch.json ...   # again
imported 0 dispatch row(s) into fleet-console
```

The old dispatcher's GitHub state carries `dispatched` only (no `attempts`, `nudges` or `closers` keys); the
import maps those when present and ignores the rest. The imported dispatch stays open, so the next tick sees the
item as in flight.

### Old timer

```
$ systemctl --user is-active gh-autodispatch   -> inactive
$ systemctl --user is-enabled gh-autodispatch  -> static
```

The old GitHub timer is not running and is not enabled, so no double dispatch is possible. The kill switch for the
new unit is `systemctl --user stop fleet-console && systemctl --user disable fleet-console`.

### Quota, real reading

`rig provider signals --json` on this box returned 9 signals and **0** seats with an unknown source inside the
5-minute window, so no quota decision was raised. The simulated two-stalled-seat test
(`internal/quota`) raises exactly one decision, pauses the project and writes `quota.decision` + `quota.pause`;
a repeat inside the window writes nothing.

### Telegram

One client, two kinds of message, and they behave differently on purpose.

**A decision is editable.** `internal/alert` sends one message per decision and edits that message on later
breaches (`TestEscalationSendsOnceThenEdits`: one send, then one edit of message id 42 from the test Bot API
server). Editing is right here because a question has one current answer — a second unanswered copy of the same
question is noise.

**A progress report is a photograph.** After a merge, `--notify-report`, or the optional digest, the service
renders the whole state and sends it as a new message. It is never edited, because editing yesterday's report
lies about what was true yesterday. Repeats are prevented by a hash of the rendered body, not by editing: the
same state sends nothing, a changed state sends one new message.

Both read `TELEGRAM_BOT_TOKEN` and `TELEGRAM_CHAT_ID` from the environment; no token is stored or logged. With
either unset the service prints one line at boot —
`notify: TELEGRAM_BOT_TOKEN/TELEGRAM_CHAT_ID not set; completion notices are off` — and every trigger becomes a
no-op. That is a configuration, not an error.

A real message id is **not** available here: neither variable is present in this seat. The transport is proven
against a fake Bot API server instead (see the PR's `--notify-report` transcript), and `TELEGRAM_API_BASE`
exists so a self-hosted Bot API server — or that test — can replace `api.telegram.org`.

### End-to-end item

Not run from this seat: a builder never merges, and the act loop is not wired into the server until this cutover
is signed off. The components are proven separately — the merge watchdog's five refusals and fake docs-only merge
(`internal/act`), the dispatch no-op, the verdict capture and the post-merge `Refs` close. The reviewer/owner runs
the one-item end-to-end with the kill switch above.
