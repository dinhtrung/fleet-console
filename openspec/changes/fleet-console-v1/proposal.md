# Proposal — fleet-console v1 (revised after the OpenRig capability review)

## Why

The 2026-10-27 design treated the app as a second system of record: its own `queue_row`, `decision`, `lease` and
`event` tables beside a JSON dispatcher. A review of OpenRig 0.7.x shows the rig already owns most of that and
maintains it: `rig queue` is the durable owned-work record with transactional handoff, closure reasons and a
transition log; `rig queue create --destination <human>@external --human-questions-file` is a decision record with
options, an SLA tier and a receipt; `rig workflow` projects the next step when a seat closes one; `rig view show
pickup|escalations|held` derives the three kinds of idle; `rig parked` diagnoses a stopped seat that still owes
work; `rig provider` owns account switching.

A second queue and a second decision store would duplicate state, drift on every rig release, and leave "who owns
this row" unanswerable. What the rig cannot do is what this app is for: it does not read the forge, it has no
ordering or blocking rules over issues, it has no merge gate, and it has no cross-project view tied to forge tasks.

## What changes

- **rig is the system of record for work state.** Queue rows, human decisions, leases, step progression and the
  parked diagnosis are read from and written through the rig CLI. The app stores what the rig does not have: a
  forge mirror, one dispatch record per attempt, the verdict it accepted, and an audit row per write.
- **The registry owns the rig lifecycle.** A project row renders a rig spec from one template, launches it, grows
  it, probes it and tears it down with `--delete`. Onboarding a project is a row; no rig is authored by hand.
- **A forge abstraction with two adapters.** GitHub (`gh`) and GitLab (`glab`) are normalized to one item /
  change-request / checks model. This repository's own fleet runs on GitHub issues; the existing fleets stay on
  GitLab.
- **One static Go binary.** Go 1.25, Fiber v3, SQLite through `modernc.org/sqlite` (no cgo), the built board
  embedded with `go:embed`. Host tools the binary drives are declared, not bundled: `rig`, `gh`/`glab`, `git`, `tmux`.
- **The rig contract is probed, not assumed.** One adapter per CLI command, a version floor checked at boot, a
  minimal-field contract, and recorded fixtures per rig version — the rig's `--json` shapes changed between 0.7.0
  and 0.7.4 in the same month.
- **The board is shadcn/ui on the sage appearance only.** shadcn/ui components (CSS-variable tokens, Base UI
  primitives) are the component library; the inherited light, dark, dusk and ember appearances are deleted and the
  appearance switcher goes with them.
- **The stack installs from a script.** A separate change set (`stack-provisioning`) adds `install.sh`, a pinned
  `stack.lock`, profiles and a verify phase, because a new box needs ten tools, the rig daemon, seat credentials
  and systemd units before this app can start.

## Capabilities

### New Capabilities

- `forge` — normalized issues, change requests and checks over GitHub and GitLab.
- `rig-lifecycle` — render, launch, grow, verify and remove a project's rig from its registry row.
- `runtime-compat` — the rig CLI contract, version floor, boot probe and fixtures.

### Modified Capabilities

- `dispatch` — same rules; idempotency and handoff now read rig rows instead of a mirrored table.
- `project-registry` — a project row owns a rig lifecycle, not only a rig name.
- `console-ui` — the board keeps its "why is it not moving" duty; shadcn/ui and sage replace the inherited
  components and appearances.
- `audit-and-alerts` — every write is still one audit row; the writes themselves go through the rig CLI.

## Impact

- New Go module (`cmd/fleet-console`, `internal/{store,forge,rig,git,decide,act,web}`) plus `web/` built to `dist/`
  at build time and embedded at compile time.
- New dependencies: Fiber v3, `modernc.org/sqlite`, Go 1.25; web gains shadcn/ui, `class-variance-authority`,
  `tailwind-merge`, `clsx`, Base UI primitives and `tw-animate-css`.
- Deleted from the web scaffold: the light, dark, dusk and ember token blocks, their registry entries, the
  multi-item appearance menu, and the four inherited component files shadcn replaces.
- Host prerequisites, declared rather than implied: `rig`, `gh` or `glab`, `git`, `tmux`, a running OpenRig daemon.
- Migration: the existing dispatcher runs in shadow until the app's plan matches it on every tick.

## Non-goals

- No model in the dispatch path; ordering is the owner's written rules.
- No replacement for the rig daemon or the forge; the app drives both through their CLIs.
- No second queue, no second decision store, no second lease.
- No cost dashboard; `rig usage` answers that on demand.
