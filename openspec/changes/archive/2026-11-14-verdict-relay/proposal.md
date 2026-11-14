# Proposal — a review that is not a verdict is relayed, not dropped

## Why

The merge gate reads exactly one shape: a note whose first line is `ACCEPT <sha>` / `REJECT <sha>`
(`internal/act/verdict.go:114-153`). Everything else is silence — and a seat does not have to be broken to
produce it:

- `#287` carried `ACCEPT 7f76634` (a 7-char prefix) at the head and the gate stored nothing; the tick read
  `#283 merge waiting: refusing merge: no ACCEPT verdict for dispatch 133` for twenty minutes. `verdict-relay`
  cannot fix that instance — the abbreviation epic does — but it is the same class.
- A review written as prose ("looks fine, one nit in the retry loop") is not a verdict at all. The app waits, then
  `decide` raises `decision.no_verdict` to the **human** (`internal/decide/plan.go:320`), who must open the change
  request, read a comment, and type a verdict himself.
- A reviewer that ends its row having posted nothing is treated identically: the same human question, minutes
  later, with no signal that the fleet is stuck on a format.

So every review that is not born in the grammar costs the operator a manual read. The grammar itself is not the
problem — the merge gate needs a deterministic record. The problem is that **nothing converts a finished review
into that record**, and a model seat is the one thing on the box that reads prose reliably and acts.

## What changes

A per-project `verdict_relay` block, off unless configured, with two modes:

- `mode: scribe` — a cheap seat **inside the project's own rig** (`verdict-scribe@<rig>`) is asked once per
  (item, head) when a reviewer row ended without an attributable verdict. Its brief carries the review's notes
  verbatim, the full head sha and the exact command; it posts **one** canonical note, or answers `INCONCLUSIVE`.
- `mode: operator` — the same trigger, plus `decision.no_verdict` questions are addressed to the configured seat
  (e.g. `operator-agent@kernel`) instead of `human@kernel`. The kernel operator reads the review and posts the note.

In both modes the relay **never merges**: it writes the record, and the existing gate merges. A relayed verdict is
marked with a second line `via: <seat>`, which the app stores, so a reader can always tell a reviewer's own verdict
from a relayed one.

## Losses, named

- **A model still decides what a review meant.** The `via:` marker makes that visible, but it does not make it
  right: a scribe can upgrade "fine, but…" into `ACCEPT`. The brief forces it to quote the sentence it acted on, so
  the wrong call is auditable after the fact — not prevented.
- **One extra queue row and one model turn per stuck review.** A project that never misformats pays nothing (the
  relay only fires when a verdict is missing after the review row ended).
- **The relay is not instant.** It fires after the reviewer's row ends, not when the note is posted, so a review
  that lands as a row is closing still gets one relay round.
- **Operator mode widens what the kernel seat touches.** That seat is shared by every rig on the box; addressing
  fleet questions to it means its queue now carries project work. It is opt-in per project.

## Capabilities

### New Capabilities

- `verdict-relay` — convert a finished, non-conforming review into a canonical verdict through a configured seat,
  with the merge gate untouched.

## Impact

- `internal/config`: a per-project `verdict_relay` block (`mode`, `seat`), strict decode, `--print-config` output.
- `internal/decide`: one new intent (`IntentRelayVerdict`) and its trigger, next to the existing `decision.*` arms.
- `internal/act`: the raise (brief with the notes verbatim + the command), the operator destination switch, the
  `via:` marker in capture, and the `verdict.relayed` audit event.
- `docs/fleet-console.md` (the verdict section), `docs/quickstart.md` (the block to copy).

## Out of scope

- Changing the note grammar, the gate's four facts, or the REJECT asymmetry.
- Letting any relay merge, close a change request, or comment anything but its one verdict note.
- A relay for a head the reviewer never saw (`decision.stale` already asks for that re-review).
