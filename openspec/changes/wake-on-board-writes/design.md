# Design — a board write asks the mirror loop for a tick

## D1. The existing Waker, not a second mechanism

`mirror.Waker` is already the loop's "tick now" channel (`internal/mirror/waker.go`), already wired into the HTTP app
as `web.Config.Waker`, and already used by the forge receiver. The fix brings the board routes onto it:

```go
func wake(w *mirror.Waker) {
    if w != nil {
        w.Wake()
    }
}
```

`Wake` is a non-blocking send on a capacity-1 channel: a handler can never be stalled by a loop that is mid-tick, and
a burst of writes coalesces into one tick. A nil waker is safe, which matters for the one-shot commands that build
the same routes with no loop running.

`Rejected:` a second channel or a "dirty" flag in the app. Two request mechanisms for one loop is how a wake gets
lost; the loop already selects on exactly one.

`Rejected:` a `config.yaml` mtime check in `sources`. That is a poll answering a poll, and it would also fire on a
hand edit the operator did not intend to dispatch on.

## D2. Wake only after the write succeeded

The call sits immediately before the success response, on the same path as the store write:

| Route | Wakes after |
|---|---|
| `POST /api/projects` | the file entry and the project row are both written |
| `PUT /api/projects/{key}` | the entry is replaced and the registry applied |
| `PUT /api/projects/{key}/onhold` | the row's `onhold` flips |
| `DELETE /api/projects/{key}` | the entry is gone and the rows are deleted |
| `POST /api/items/{n}/verdict` | the verdict comment is on the change request |

A refused write (409 stale read, 422 validation, 500) wakes nothing: the loop would read the old state and only burn a
forge read. The tests pin the success case; the refusals are `TestCreateProjectStatuses` and
`TestProjectEditStatuses` already.

## D3. The verdict route belongs in this change

The tick's job after a verdict is capture → merge. Posting a verdict on the board is the operator saying "the facts
changed"; making the merge wait up to `idle_poll_max` is the same defect with a worse consequence, because the merge
gate is where the operator's decision becomes work. `Fleet` gains the `Waker` field the app already holds.

`Rejected:` leaving verdicts out because the reviewer usually posts on the forge. The forge path wakes through the
webhook; the board path is the one with no signal.

## D4. What this does not fix

The quiet loop still prints nothing between ticks, so a genuinely wedged tick (an unbounded child process, a stalled
ssh mux) still looks like an idle loop. The difference is what the operator does about it: with this change, a board
write that does not produce a tick within seconds is now evidence of a real hang rather than normal backoff. A
heartbeat line or a per-tick watchdog is a separate change.

## Tests that prove the promises

| Promise | Test |
|---|---|
| create, edit and delete each wake the loop | `TestProjectWritesWakeTheLoop` |
| a refused write does not wake | the existing status tests, plus the wake assertion's absence |
| a nil waker is safe | every one-shot command path (no waker configured) |
