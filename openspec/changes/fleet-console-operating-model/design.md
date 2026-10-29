# Design — operating model

Six decisions from the first real install (three projects, three rigs, one live act loop). The ledger lives in
`changes/fleet-console-v1/design.md` §17; this file records the reasoning that the requirements rest on.

## D1 — one instance owns the database

The app plans and writes in the same tick, so two instances over one SQLite file are not a lock contention problem,
they are two planners seeing the same idle builder. A `flock` on `<database>.lock` is the cheapest correct answer: it
dies with the process, needs no heartbeat, and a second instance fails loudly before it reads anything. A lease row in
the database was rejected because a wedged process would hold it until a timeout — the failure the lock exists to
prevent.

## D2 — a verdict belongs to a sha

`ACCEPT <sha>` is a statement about a tree, not about an item. When a builder merges `main` to pick up CI, the tree
changes and the verdict becomes evidence about the old tree. The app keeps refusing to merge (v1 already did), but
"parked" tells nobody and the item waits for a human. The fix is to make staleness a named state and to ask the
reviewer once per head. Asking once per tick would flood the reviewer; asking once per head matches how often the
situation can change.

## D3 — a project that leaves the file is disabled

The act loop iterates the database, which is right (a project must keep working while the file is unreadable), but it
means the file is not authoritative for *existence*. Applying a config disables what it does not name. `--prune` stays
the only deletion path, so an accidental edit cannot destroy the audit trail, and a live dispatch blocks it.

## D4 — the rig wins

The app's `dispatch` and `verdict` rows exist to answer "which attempt is this", not "is this item being worked on".
The rig answers the second question, so it wins every disagreement. Reconciliation runs before planning in the same
tick, writes one event per transition, and never dispatches again in the same tick — otherwise a superseded row and a
new dispatch would race in one plan.

## D5a — one project per rig, one or more rigs per project

A rig is a set of seats with launch-time cwd, MCP servers, culture and guidance. Those are per repository, so a rig
serves one project; a project may drive several rigs, each launched for its own checkout or a scratch root it owns.
The registry lists them as tiers. `rig: name` is the one-element form.

This reverses the v1 framing ("one rig per project is the default"): the isolation rule is one *project* per rig, not
one rig per project. The deployment that exposed it was `mnpserver2` pointed at the tsim rig.

## D5b — tiers are filled in order, and crossed by a trigger

Two rules that answer different questions:

- inside a tier, fill rigs in listed order (deterministic plans, warm indexes, one obvious place to look);
- across tiers, cross only when the boundary's trigger holds: `on-full` (capacity) or `on-unavailable` (health).

The trigger is the only difference between "overflow" and "standby", so it is config, not a second policy. A tier that
is full under `on-unavailable` waits: spilling there spends the backup's quota before it is needed.

Health is a stall, not a load number: a seat holding a row with no session output and no tool call for `stall_after`
(default 10m) is unhealthy. Two consecutive unhealthy ticks are required (`failover_after`, default 2) so a slow
thought is not a failover. Switching back is manual by default — an automatic return flaps while a provider recovers.

A live row is never migrated between tiers: the row belongs to the rig that accepted it, and a dead seat's row is
closed `superseded` so the item can be re-dispatched on the same branch, which `WorktreeAddOrReuse` already reuses.

## D6 — two-phase dispatch

Order matters for crash recovery, not for correctness on the happy path. Writing the dispatch record `pending` before
the queue row means a crash leaves a record that claims less than reality, which boot can repair by looking for the
row. The reverse order leaves a row that no record knows about, and the next tick creates a second one.
