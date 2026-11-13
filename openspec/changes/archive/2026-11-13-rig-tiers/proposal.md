# Proposal — rigs in tiers, with one spill trigger each

## Why

A project may name several rigs (`config.Project.Rigs []string`, `internal/config/config.go:46`), and the plan
flattens them: `mirror.BuildSnapshot` appends every running seat to `Snapshot.Builders`
(`internal/mirror/mirror.go:402`) and `decide` fills `free` in that order. Two consequences:

- **A standby rig is not a standby.** A rig listed second receives work at the same rate as the primary, so its
  provider quota is spent on overflow at the same time as the primary's — which is the opposite of keeping a
  reserve for the day the primary dies.
- **A full rig parks work instead of overflowing.** When every seat is busy the plan emits `no free builder` and
  nothing else, even though a second rig sits there with idle seats.

The operating model already decided this (`rigs: [{name, tier, spill}]`, cross a tier boundary only on its
trigger); the code has the flat list and no trigger. In practice the box has two rigs available — an omp one and a
claude one — and today the second is usable only by editing the project's list and restarting.

## What changes

- `rigs:` accepts `{name, tier, spill}` entries beside the bare string form, which means *tier 0, spill on-full*
  (today's flat behaviour, unchanged).
- The plan fills seats tier by tier. It crosses into tier N only when every earlier tier is exhausted **and** the
  entry's trigger holds: `on-full` (no free seat left, cap included) or `on-unavailable` (that rig has zero running
  seats or its queue read failed).
- A dispatch names a seat on exactly one rig, and the skip line says which tier refused it and why.

## Losses, named

- **`on-unavailable` rigs will not warm up on a busy day.** That is the point, and it also means the first dispatch
  after a primary failure starts cold (`rig up` cost lands on the item). The lifecycle change (#156) already pays
  that cost deliberately.
- **One more thing to configure.** A project that never wants its second rig used says `spill: never` and gets the
  old behaviour explicitly.
- **No cross-project balancing.** A rig a project does not list is never considered, whatever its idle seats; the
  registry's exclusivity rule (one rig, one project) makes sharing a rig a config error today.

## Capabilities

### New Capabilities

- `rig-tiers`: the tiered rig declaration, the fill order, the two spill triggers, and the rule that a dispatch
  names one seat on one rig.

## Impact

- `internal/config/config.go` — the `Rigs` union type, defaults, validation (duplicates, unknown trigger).
- `internal/configfile` + `--print-config` — the tiered form renders and round-trips.
- `internal/decide/decide.go` — the free-seat selection becomes tiered; `Result` gains the tier that refused.
- `internal/act/actions.go` — no change in shape: it already takes a seat, which now names its rig.
- `docs/my-stack.md` — the two triggers and a worked example.
