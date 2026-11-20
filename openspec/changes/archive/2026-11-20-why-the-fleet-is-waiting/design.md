# Design — why the fleet is waiting

## D1. Read the attention facts the rig already sends

`rig status --json` carries `needsAttention`, `arbitratedNeedsInput` and `needsInputEvidence` per node (the compat
probe names all three as unknown keys in rig 0.7.13). The adapter's decode is additive — unknown keys are ignored
today — so the change is to model them, not to change the call. One read per tick, no new process.

Each seat becomes:

```go
type SeatAttention struct {
    Seat     string // canonical session name
    Rig      string
    Kind     string // "attention" | "input" | "arbitrated"
    Question string // the text the rig reports, trimmed; "" when the rig gives none
    Since    time.Time // first tick this app saw the question
}
```

`Since` is the app's own memory (`attention` map on the coordinator, like `quiet`), because the rig reports the state,
not its age. A restart forgets it and the age starts again — a display regression, never a wrong decision.

`Rejected:` parsing the rig's UI text to guess the question's meaning. The board shows the text; it does not classify
it.

## D2. One obligation per question, resolved when the question is gone

The fingerprint is `seat + hash(question)`. The first tick that sees it writes one `attention.raised` event and one
notice; every later tick with the same fingerprint writes nothing. When a read returns the seat without that
fingerprint, the app writes `attention.resolved` — one event, no notice.

Absence must be **observed**, not inferred: a failed rig read says nothing about the question. Only a successful read
that lacks the fingerprint resolves it (`RigStale` → no resolution), the same rule that keeps a stale read from
vouching for demand.

`Rejected:` resolving on a timer. A question answered at minute 41 and a question never asked look identical to a
clock.

## D3. The capacity line names the human

The dispatch reason `no_free_builder` gains a detail when at least one admitted seat is `attention_required`:
`every admitted seat is taken (1 waiting on a human)`. The detail is computed where the seats are already known
(`decide.fillSeats`), so the plan and the board cannot disagree.

## D4. The last plan reason survives the tick

`decide.Result.Skips` is written once per tick to the store: `(project, item, reason, detail, head, tick_at)`,
upserted per item so the table stays one row per item, not one per tick. The board reads it for the item's why-column.
A skip that disappears (the item dispatched) deletes the row in the same tick.

`Rejected:` keeping it only in memory. The board is served by the same process, but a restart is exactly when an
operator opens it to ask what happened.

## D5. The panel links, it does not act

The waiting panel shows seat, rig, question and age, and links to the rig's UI URL (`rig status` prints it). There is
no approve control: a board that can approve an agent's permission prompt is a board that can grant an agent anything
by accident.

## Open question for the operator

Three kinds of attention are reported and the rig's own semantics for them are not documented here. The design treats
all three as "a human is needed" and shows `Kind` verbatim; if one of them turns out to be informational rather than
blocking, it should be dropped from the panel by name rather than by heuristic.
