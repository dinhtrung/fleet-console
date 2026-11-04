# Design — quiet state and where the gate lives

## D1. Quiet is a property of the snapshot, computed once

`progress.Snapshot` already carries `Counts` (true size of every bucket), `InFlight`, `Pending`, `OnHold` and the
project row's own decisions come from `mirror.TickResult` rows. The predicate therefore needs no new input:

```
Quiet() = Counts.Epics == 0 && Counts.InFlight == 0 && Counts.Pending == 0 &&
          Counts.OnHold == 0 && Counts.OpenDecisions == 0
```

`Counts` is used rather than the slices because lists are truncated for rendering; a truncated list must never make
a busy board look quiet. If `OpenDecisions` does not exist yet on `Counts`, it is added there and filled where the
snapshot is built — one place, so the renderer and the predicate cannot disagree.

Rejected: parsing the rendered text for "no open work". The string is a presentation detail, it is built from the
same state the predicate can read directly, and a wording change would silently disable the mute.

## D2. The gate belongs in the notifier, not in the callers

`cmd/fleet-console/main.go` decides *when* a trigger applies (`len(report.Merges) > 0 || digest.allow(read.At)`); it
must keep doing only that. `sendProgress` already owns "should this state be reported", because the dedupe lives
there. Adding quiet to the same function keeps one answer to one question, and it means a future trigger cannot
forget the rule.

Rejected: gating in `main.go` — a second trigger added later would bypass it, and there would then be two rules to
keep in sync.

## D3. A skipped notice must not touch the dedupe record

`alert.Notifier.Notify` records the message id of the last body it sent. The quiet path returns **before** rendering
and before `Notify`, so nothing is recorded. That is what makes the "work resumes" scenario hold: after a quiet
stretch the dedupe still points at the last real message, and the next real body differs anyway.

The alternative — render, then compare, then skip — would record nothing either, but it would also render a body
nobody sends and tempt a future reader into thinking the message went out.

## D4. No configuration knob

The operator's escape hatch is `--notify-report`, which is explicit and leaves a log line. A `notify_quiet`
setting would re-add the noise by default-on-in-practice and would need its own tests, docs and a place in the
config identity hash. The owner asked for silence, not for a knob.

## D5. What stays exactly as it is

- The board and the TUI keep rendering the idle reason (rule 5 of `idleReason`), because that is where the
  explanation belongs.
- Escalations (`alert.Escalator`) are untouched: an open decision makes the snapshot non-quiet, so the rule can
  never swallow one.
- `notify.configured() == false` still short-circuits first: a box without credentials must log the boot line and
  send nothing, whatever the board holds.
