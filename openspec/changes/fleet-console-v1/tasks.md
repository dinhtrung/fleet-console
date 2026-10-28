# Tasks — fleet-console v1

Each phase ships on its own. A box is ticked only with the command that proved it. The order is deliberate: the
rules and the oracle come before any surface (design §15.8).

## 1. Contract, skeleton, rules port (P0)

- [x] 1.1 Go module `github.com/dinhtrung/fleet-console`, `cmd/fleet-console`, `internal/{store,forge,rig,git,decide,act,web}`, Go 1.25. Verify: `go build ./...` and `go vet ./...` exit 0.
- [x] 1.2 Fiber v3 app skeleton: `/api/health`, `/api/compat`, bearer middleware, `Idempotency-Key` middleware, graceful shutdown on SIGTERM. Verify: `go test ./internal/web` covers 401 without token, 200 with, and a repeated key returning the first `event_id`.
- [x] 1.3 SQLite store with the schema of design §6 (`modernc.org/sqlite`, WAL, `busy_timeout`), migrations, one writer guard. Verify: `go test ./internal/store` creates, reopens and migrates a temp DB; `CGO_ENABLED=0 go build ./...` succeeds.
- [x] 1.4 Port the dispatcher's rules as pure functions over a snapshot: epic skip, parent gate, `onhold` cascade, live-row-body idempotency, branch-on-origin, one-way blocking, ascending order, cap. Verify: table tests, one per rule, named after the rule.
- [x] 1.5 Oracle harness: run `~/bin/fleet-autodispatch.py --dry-run` and the Go plan against the same recorded inputs; diff task sets and skip reasons. Verify: `go test ./internal/decide -run TestOracle` passes on at least 20 recorded ticks checked into `testdata/ticks/`.
- [x] 1.6 Rig compat probe: version floor, one real `queue list` and `ps --nodes` read, required-field checks, unknown-key log. Verify: `fleet-console --compat` prints version, floor and the two parsed shapes; a fixture with a renamed field exits non-zero naming the field.

## 2. Rig contract and lifecycle (P0)

- [ ] 2.1 Rig adapter: `QueueList`, `QueueShow`, `QueueCreate`, `QueueUpdate`, `QueueHandoff`, `QueueResolve`, `Seats`, `Parked`, `ProviderSignals` — one function per CLI command, domain types out. Verify: fixture tests per command against captured `--json`; no `map[string]any` crosses the package boundary (`go vet` + a grep task in review).
- [ ] 2.2 Spec template `templates/rig.yaml.tmpl` + renderer: rig name, per-seat cwd/runtime/model, account env, builder count, guidance and culture paths. Verify: `go test ./internal/rig -run TestRenderSpec` compares the rendered YAML with a golden file, and `rig spec validate <rendered> --json` reports valid.
- [ ] 2.3 Launch sequence: write spec dir, `rig specs sync`, `rig up <rig> --plan --json`, `rig up --non-interruptive`, verify every seat `running` or fail with the seat named. Verify: one real launch on the fleet box, with the JSON of both calls recorded in the task's evidence.
- [ ] 2.4 Growth and write-back: `rig grow` then `rig export` so the spec matches the running topology. Verify: after growing one seat, the exported spec validates and `rig ps --nodes --rig <rig> --json` lists the new seat.
- [ ] 2.5 Teardown: `rig down <rig> --delete` + spec archive; refuse when the rig still has live rows. Verify: a throwaway rig is removed and `rig ps --include-archived` no longer lists it; `rig up <same-name>` fails with a row-count message before teardown and succeeds after.
- [ ] 2.6 Per-seat credential and MCP seeding before launch (provider key, `mcp.json` with serena/codegraph/rtk entries, `PATH` including Go). Verify: from inside a launched seat, `rig whoami --json` and one `mcp__serena__*` call succeed; the key never appears in any log or event row (grep the event table).

## 3. Forge adapters (P1)

- [ ] 3.1 Normalized model + interface (`Item`, `ChangeRequest`, `Check`, writes) with a fake implementation. Verify: `go test ./internal/forge` exercises the interface through the fake for decide-loop tests.
- [ ] 3.2 GitHub reader: issues, sub-issues (epic → children), issue dependencies (`blocked_by`), PRs, check runs / commit statuses. Verify: recorded payload fixtures from `dinhtrung/fleet-console`; `TestGitHubItems` reproduces an epic with children and one blocked task.
- [ ] 3.3 GitHub writes: label set/clear, comment, close item, `gh pr merge --squash --match-head-commit <sha>`. Verify: one real issue labelled and one docs-only PR merged by the watchdog path on a throwaway branch.
- [ ] 3.4 GitLab reader/writer parity: `glab api` items with `related_items` (smaller-iid rule), MRs, head pipeline jobs with `allow_failure`, `glab mr merge --sha`. Verify: fixture tests from `traffic-simulator-v2` payloads, and the same decide test passes for both adapters.
- [ ] 3.5 Auth and rate limits: token from the CLI's own store (never the repo), one retry on 5xx, a bounded per-tick request budget. Verify: `TestBudget` refuses the 101st call in a tick; a 401 surfaces as a project-level `stale` state, not an empty board.

## 4. Mirror and decide (P1, dry-run)

- [ ] 4.1 Mirror loop with jitter, per-project stamps and a `stale` flag. Verify: kill the forge CLI and confirm the API still returns the last successful sync time plus `stale: true`.
- [ ] 4.2 Branch index per repo (`feature/<n>-*`) from `git ls-remote`. Verify: `TestBranchIndex` over a recorded remote listing.
- [ ] 4.3 Rig snapshot read (`queue list -A -a`, `ps --nodes --full`) into the `snapshot` cache, stamped. Verify: the board endpoint returns rows and seats from the snapshot with `synced_at`.
- [ ] 4.4 Housekeeping passes: prune, reset stalled (≤3), marker reconcile, re-nudge (15 min / 30 min cooldown), epic close. Verify: one test per pass asserting the `event` rows written.
- [ ] 4.5 Incident fixtures: parked (open CR, no verdict, no live row) and marker leak (merged CR, item open) raise the decision question. Verify: `go test ./internal/decide -run TestIncident` names the question text for both fixtures.
- [ ] 4.6 Dry-run week: intents only, compared against the live dispatcher every tick. Verify: ten consecutive ticks with zero diffs, recorded in `docs/cutover.md`.

## 5. Act (P2)

- [ ] 5.1 Dispatch intent: fetch, base sha, worktree add-or-reuse, brief from template, `rig queue create --source human@kernel --destination <seat> --body-file`, marker label, dispatch row. Verify: a real issue dispatched with the old timer stopped; a second run is a no-op naming the existing row.
- [ ] 5.2 Row actions: resolve, handoff, close-as-superseded, re-nudge — each one rig call plus one `event` row. Verify: each action's test asserts the CLI argv, the exit code and the before/after state.
- [ ] 5.3 Verdict capture: read the reviewer's note, store `verdict(dispatch, sha, accept|reject)`; only the newest note counts. Verify: three tests — accept, later reject vetoes, note for an older sha ignored.
- [ ] 5.4 Merge watchdog: four facts re-read in the act pass; refuse naming the missing fact. Verify: four refusal tests plus one end-to-end merge on a docs-only PR.
- [ ] 5.5 Post-merge: verify the item closed, close it when the body only referenced it, clear the marker, unblock the next item. Verify: a fixture where the PR body says `Refs #n` and the item ends closed with an event naming the reason.

## 6. Web foundation and board (P3)

- [x] 6.1 shadcn/ui init: `components.json`, `@` alias, Base UI primitives, `tw-animate-css`, `cva`/`clsx`/`tailwind-merge`. Verify: `npm run typecheck && npm run build` green, and one generated component (`button`) renders in the existing shell.
- [x] 6.2 Sage as the only appearance: delete the light/dark/dusk/ember token blocks, their registry entries, the appearance menu and the `dark` custom variant; `:root` carries sage. Verify: `grep -rn 'data-theme="\(light\|dark\|dusk\|ember\)"' src index.html` returns nothing; a screenshot shows the sage board.
- [x] 6.3 Token bridge: map shadcn semantic variables onto the sage roles (`--background`, `--foreground`, `--primary`, `--border`, `--ring`, `--radius`), no component-level colour overrides. Verify: the board and a shadcn `Dialog`/`Menu` use only bridge variables (`grep -rn 'bg-\|text-' src/components/ui` shows token classes, not palette hex).
- [x] 6.4 Replace the inherited primitives that shadcn now owns (Button, Dialog, Menu, Field, Toast) and delete the replaced files. Verify: no screen imports a deleted path (`npx tsc -b` green) and the accessibility checks on the swapped Dialog/Menu pass.
- [ ] 6.5 Embedded board: `go:embed dist`, SPA fallback, SSE at `/api/stream`. Verify: `go build` then `./fleet-console` serves the board with no Node at runtime; `curl -N /api/stream` prints one event on a state change.
- [ ] 6.6 Board, decisions, seats and task drawer screens. Verify: one screenshot per screen on sage tokens — two projects, one paused, one parked decision; the seat screen shows a real omp seat; the drawer shows branch, PR, checks, verdict and events.

## 7. Alerts, quota, multi-project, cutover (P4)

- [ ] 7.1 Telegram escalation on SLA breach and on `quota`: one message per decision, edited rather than repeated. Verify: a forced breach delivers one message with project, item, question and a board deep link.
- [ ] 7.2 Quota handling: two seats stalled within 5 minutes raises one decision and pauses dispatch to that rig; `rig provider signals` is the source. Verify: simulated stall test and one real reading.
- [ ] 7.3 Second project onboarded with its own generated rig and isolated capacity. Verify: both projects dispatch without a shared worktree path or item-number collision.
- [ ] 7.4 Cutover: import the dispatcher state (`dispatched`, `attempts`, `nudges`, `closers`), disable the old timer, run one item end-to-end. Verify: item dispatched → reviewed → merged → epic closed, with the board empty and a reason per closed item; `docs/my-stack.md` names the binary, the unit, the port and the kill switch.
