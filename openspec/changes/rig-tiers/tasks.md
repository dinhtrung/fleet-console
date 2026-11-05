# Tasks — tiered rigs

## 0. Ground rules

- Gate: `go build ./... && go vet ./... && go test ./...` and `openspec validate --all --strict`. Real output.
- Fixed vocabulary: keys `name`, `tier`, `spill`; trigger values `never`, `on-full`, `on-unavailable`; skip reason
  keeps `no_free_builder` and gains a `tier N (spill=…: …)` detail.
- No change to the concurrency cap, the worktree layout or `Actions.Dispatch`'s signature.
- `internal/config/config.go` is shared with #157 and with the settings task; rebase on `main` before opening the PR.

## 1. Declaration (builder 1)

- [x] 1.1 `internal/config`: `RigRef{Name string; Tier int; Spill string}` with a union `UnmarshalYAML`; defaults
      `tier 0`, `spill on-full`; validation for a duplicate name, a negative tier and an unknown trigger;
      `--print-config` renders the resolved form.
      Evidence: tests for the scalar form, the mapping form, the defaults, the three refusals, and a config file
      that round-trips through `configfile.UpsertProject`.
- [x] 1.2 The back-compat test: `rigs: [a, b]` resolves to two tier-0 entries and the resolved project is equal to
      what `main` produces today for the same file (field-by-field).

## 2. The fill order (builder 1, after 1)

- [x] 2.1 `internal/decide`: group free seats by the `@rig` suffix of the session name; walk `Rigs` in order; apply
      the trigger; expose the chosen seat. Keep `freeBuilders`/`busyBuilders` as the primitives.
      Evidence: a table test — free seat in tier 0 wins; tier 1 used on `on-full` overflow; not used when
      `on-unavailable` and tier 0 is running; used when tier 0 has zero seats; a foreign seat ignored; the cap
      counting as "full".
- [x] 2.2 Skip detail `tier N (spill=<trigger>: <fact>)` on every `ReasonNoFreeBuilder`, with the fact that refused
      it. Evidence: the exact skip strings in the PR body.

## 3. Wiring and proof (builder 2, blocked by 2)

- [x] 3.1 `internal/act`: assert the created row's destination is `<seat>@<rig>` and that the worktree is still
      `wt-<key>-<n>` (no per-rig layout change). If a code change is needed, it is in the row request only.
- [x] 3.2 End-to-end with `forge.NewFake` and a fake rig: two rigs, tier 0 full, tier 1 free — one dispatch, one
      row, on a tier-1 seat, and the journal line.
- [x] 3.3 `docs/my-stack.md`: the two triggers, one worked example, and the warning that `on-unavailable` starts the
      standby cold.
