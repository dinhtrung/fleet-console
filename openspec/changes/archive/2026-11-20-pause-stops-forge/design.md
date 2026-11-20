# Design — pause stops all forge traffic

## D1. Guard at both boundaries: the mirror's project spec and `RunTick`

The pause flag reaches the read pass as `mirror.Project.Paused` (set once, in `projectSources`), and the act pass
as the project row that `RunTick` already receives. Either guard alone leaves a hole: `projectSources` is also
called by the one-shot `tick` command and by `runHousekeep`, and `RunTick` is called by the loop and by tests.

Rejected: a `continue` in the serve loop only — the dry-run command and housekeeping would still read the forge,
and the next caller of `RunTick` would inherit the gap.

## D2. A paused project skips the git read too

`Mirror.Tick` reads the local branch index for every project (`internal/mirror/mirror.go:297`). A paused project
skips it as well: no plan can move, so the index is not read, and the subprocess per paused project per tick is
pure waste.

Rejected: keep the branch snapshot fresh while paused — it costs a git process per project per tick and no
reader of a paused project can act on the result.

## D3. Keep the fleet-wide rig reads and the roster writes

The queue and roster reads are fleet-wide (`Mirror.Tick` reads them before the project loop), so skipping them
per project saves nothing. Their per-project result — rows, seats, `RigWarnings`, `RigUnknown` — is what the
lifecycle uses to decide a stop. A paused project that contributed no seats would make a running rig look empty.

Rejected: drop the paused project's rig facts along with its forge facts — the idle rule would see `running=0` on
a live rig and the plan for the resume tick would start from a false roster.

## D4. The board verdict route refuses while paused

`internal/web/verdict.go:44` posts the ACCEPT/REJECT note immediately and wakes the loop. If a paused project
accepted the note, the operator would see "verdict posted" and no merge until resume — a half action that reads
as a bug. The route answers `409` and writes nothing.

Rejected: allow the note and let it wait — it puts a verdict on the forge that no tick will read while paused, so
the operator's action silently does nothing. Rejected: allow the note *and* merge — that is exactly the forge
write the pause forbids.

## D5. The header says paused, not stalled

`mirror_read.go` judges a stamp against the poll cadence; a paused project's stamp stops advancing, so within
minutes the header reads `last forge read 40m · stalled`. A red state for a deliberate pause trains the operator
to ignore the one that is real.

Rejected: leave `stalled` — the mirror is healthy; it is doing exactly what the pause told it to do.

## D6. One flag, no pause source

`onhold` is set by the board, by a quota decision (`internal/quota/quota.go:96`) and by a failed delete. All three
mean "do not work this project", so all three get the same freeze. Resume is the operator's act in every case.

Rejected: add an `onhold_source` column — a schema migration to distinguish two pauses whose observable meaning is
identical, and a second rule to keep the two in sync.

## D7. No digest while paused

The progress digest is built and sent inside the act phase (`cmd/fleet-console/main.go:1000`). A paused project
no longer enters that phase, so it sends nothing. The board already carries the paused state; a periodic message
saying "paused" adds a second surface that can disagree.

Rejected: move the digest out of the act phase so paused projects keep reporting — a second code path for a
message that says nothing changed, and one more thing that must know about pause.

## D8. Housekeeping skips a paused project

`runHousekeep` builds its four keep conditions from a fresh forge read. With no read, the change-request list is
empty, and an empty list is the condition that lets a worktree be reclaimed. Skipping the project is the only
safe answer; the journal line makes the skip visible.

Rejected: keep the cached change requests and let housekeeping judge on them — a cache that is hours old would
authorise deleting a worktree whose branch has moved.
