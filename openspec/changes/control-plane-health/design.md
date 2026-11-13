# Design — the control plane can say whether it is working

## D1. Liveness stays cheap; readiness names the fact that fails

`/api/health` is what a supervisor or a probe polls every few seconds. Answering it with real checks would restart
the service during a slow forge read — the worst possible time. So `/api/health` keeps its literal `{"status":"ok"}`
("the process is up and serving") and a new `/api/ready` answers the operator's question: **can this control plane do
its job right now?** It returns 200 only when every fact holds, and 503 with the failing facts otherwise:

- `database`: a `SELECT 1` against the store;
- `mirror.<project>`: age of `last_forge_at` and `last_rig_at` from `mirror_meta` against `2 × tick_interval`
  (amber is the board's job, red is readiness);
- `queue`: the last `rig queue list` error and its time — cached for 30 s, because a probe must not shell out on
  every request;
- `tick`: the age of the last completed tick per project, from the same place the notifier reads it.

`Rejected:` one endpoint that is both. A restart-on-failure policy wired to a readiness check turns a slow dependency
into a restart loop, and the operator then reads "crash" where the system was busy.

## D2. Staleness is shown from the heartbeat that already exists

`mirror_meta` is written on every successful read and read by nothing. The project header shows
`last forge read <age>` and `last rig read <age>`, coloured at `2 × tick_interval` (amber, "quiet") and
`5 × tick_interval` (red, "stalled"), raised to the poll's own window when `rig_lifecycle.idle_poll_max` is longer:
a quiet box writes a stamp once per window, so `2 × tick_interval` alone would paint a healthy idle box stalled
(#408, the same derivation `/api/ready` uses). No new writes, no new table: the surface reads the row the mirror
already keeps.

`Rejected:` computing staleness from the newest event. An idle project writes no events, so the honest "nothing has
happened" would render as "stalled", which is the false alarm this change exists to remove.

## D3. Retention is bounded, auditable, and refuses to remove a live record

Housekeeping gains a retention step: `events_keep_days` (default 90, `0` disables), deleting in batches (`LIMIT 500`)
so a first run on a large ledger cannot hold the write lock for a minute. Two guards:

- rows for an item with a live dispatch (no `ended_at`) and rows for a project whose queue has an unresolved row are
  **never** deleted, whatever their age — the ledger is evidence, and evidence for open work outlives the window;
- the step is dry-run by default, like every other housekeeping step, and writes one `housekeeping.retention` event
  per run with the counts (`scanned`, `deleted`, `kept_live`).

`Rejected:` deleting by row count ("keep the newest N"). A quiet month and a busy week would keep different amounts of
history for the same work, and the audit would stop answering "when did this merge".

## D4. The reviewer seat is not in this change set

`internal/act/review.go:53,130` hard-codes `"dev-review@" + rigName`. The rig filed #384 for it and closed it without a
merge; the literals are still on `main`. It ships as its own three-line PR with a config key and a fallback, because
this change set is about telling the truth, not about routing.
