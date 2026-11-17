# Changelog

All notable changes to fleet-console are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Entries link to the merged pull request that shipped the change where one
exists. Dates are the release date in UTC. The changelog lists notable,
user-facing work; routine test, CI, deploy and OpenSpec changes are folded into
**Internal** or omitted. Each version heading links to the full compare range.

## [Unreleased]

### Added

- **Pausing a project now stops all forge traffic.** A paused project is not
  read by the mirror, its act tick does nothing, the loop and housekeeping skip
  it, and the board refuses a verdict with a `409` that says the project is
  paused ([#516](https://github.com/dinhtrung/fleet-console/pull/516), [#518](https://github.com/dinhtrung/fleet-console/pull/518), [#520](https://github.com/dinhtrung/fleet-console/pull/520), [#521](https://github.com/dinhtrung/fleet-console/pull/521)).
- The docs describe what a pause stops and what keeps running
  ([#522](https://github.com/dinhtrung/fleet-console/pull/522)).

### Changed

- The board shell and document head describe this console rather than a demo
  admin panel ([#514](https://github.com/dinhtrung/fleet-console/pull/514), [#515](https://github.com/dinhtrung/fleet-console/pull/515)).
- The mock data layer and its assets are gone; every screen reads a real
  surface ([#517](https://github.com/dinhtrung/fleet-console/pull/517)).

### Internal

- CI refuses demo scaffolding in the web tree ([#519](https://github.com/dinhtrung/fleet-console/pull/519)).

## [v0.4.0] - 2026-11-16

### Added

- **Epic integration branches.** With `epic_branches: true`, an epic's children
  land on `epic/<n>-<slug>` instead of the default branch. The first child
  creates the branch from the default head, every later child is cut from the
  epic branch, and the child's brief names the base it must target
  ([#492](https://github.com/dinhtrung/fleet-console/pull/492), [#493](https://github.com/dinhtrung/fleet-console/pull/493), [#494](https://github.com/dinhtrung/fleet-console/pull/494), [#495](https://github.com/dinhtrung/fleet-console/pull/495)).
- **The merge gate enforces the epic base.** A child whose change request
  targets the wrong base is refused with `wrong_base`, and the epic's own change
  request is never merged ([#496](https://github.com/dinhtrung/fleet-console/pull/496)).
- **One epic change request when the last child closes.** The app opens a
  non-draft `epic/<n>-…` → default change request listing every child under
  `Merged` / `Not merged` and `Closes #<epic>`; rollup and the epic sweep hold
  the epic open until a human merges it ([#497](https://github.com/dinhtrung/fleet-console/pull/497)).
- **A New project dialog bounded to the viewport.** Title and actions stay
  visible while the form body scrolls, and the live preflight collapses to one
  summary line with `Show details` for the four checks ([#483](https://github.com/dinhtrung/fleet-console/pull/483)).
- **GitLab epics are recognised by `type::epic`** before the hierarchy read, so
  a labelled epic with no children is no longer dispatched as a task ([#480](https://github.com/dinhtrung/fleet-console/pull/480)).
- The waiting-on-human list is filled from the seat read, so
  `no_free_builder` can name a seat waiting on a person ([#469](https://github.com/dinhtrung/fleet-console/pull/469)).

### Changed

- A dry run counts each page's own rows instead of a shared counter ([#470](https://github.com/dinhtrung/fleet-console/pull/470)).

### Fixed

- Board writes keep their idempotency key when the request header buffer is
  reused, so a retry is not silently a second write ([#475](https://github.com/dinhtrung/fleet-console/pull/475)).

### Internal

- The four one-task-per-epic scenarios that had no test are pinned
  ([#471](https://github.com/dinhtrung/fleet-console/pull/471)).

### Documentation

- Added `docs/epic-branches.md`, the operator runbook for the full gate
  ([#498](https://github.com/dinhtrung/fleet-console/pull/498)).

## [v0.3.0] - 2026-11-14

### Added

- **Verdict relay.** A finished review without a verdict is relayed to the
  reviewer instead of dropped. `verdict_relay` is a per-project config block,
  relayed heads reach the planner, and operator questions are routed with it
  ([#303](https://github.com/dinhtrung/fleet-console/pull/303), [#324](https://github.com/dinhtrung/fleet-console/pull/324), [#331](https://github.com/dinhtrung/fleet-console/pull/331), [#379](https://github.com/dinhtrung/fleet-console/pull/379), [#418](https://github.com/dinhtrung/fleet-console/pull/418)).
- **Project edit, pause and delete from the board.** The API and the Projects
  rows can edit, pause (`onhold`) and delete a project, with the same cascade
  as `--prune` and a refusal while a dispatch is live ([#280](https://github.com/dinhtrung/fleet-console/pull/280), [#286](https://github.com/dinhtrung/fleet-console/pull/286), [#293](https://github.com/dinhtrung/fleet-console/pull/293)).
- **Self-hosted GitLab.** `forge_host` names one host per project; every
  `glab` call site honours it, the project form carries it, and preflight names
  the host the write probe aimed at ([#330](https://github.com/dinhtrung/fleet-console/pull/330), [#333](https://github.com/dinhtrung/fleet-console/pull/333), [#334](https://github.com/dinhtrung/fleet-console/pull/334), [#337](https://github.com/dinhtrung/fleet-console/pull/337)).
- **A waiting panel and a why-column.** The project page lists unanswered
  operator questions, and the last plan reason for an item survives a restart
  ([#380](https://github.com/dinhtrung/fleet-console/pull/380), [#411](https://github.com/dinhtrung/fleet-console/pull/411), [#440](https://github.com/dinhtrung/fleet-console/pull/440), [#442](https://github.com/dinhtrung/fleet-console/pull/442)).
- **Readiness that names the failing fact.** `/api/ready` names the store read,
  mirror or queue that fails, `/api/health` stays a constant, and the project
  header shows how old each mirror read is ([#404](https://github.com/dinhtrung/fleet-console/pull/404), [#408](https://github.com/dinhtrung/fleet-console/pull/408)).
- **Ledger retention.** `events_keep_days` prunes events in batches and never
  past live work ([#407](https://github.com/dinhtrung/fleet-console/pull/407)).
- **One child of an epic in flight.** The next child starts when the previous
  lands, lowest number first, and `epic_serial: false` restores parallel
  siblings ([#446](https://github.com/dinhtrung/fleet-console/pull/446), [#448](https://github.com/dinhtrung/fleet-console/pull/448)).
- `no_free_builder` names the seats waiting on a human, from the attention
  facts the rig already sends ([#433](https://github.com/dinhtrung/fleet-console/pull/433), [#434](https://github.com/dinhtrung/fleet-console/pull/434)).
- One obligation is kept per seat question, resolved only by an observed read
  ([#437](https://github.com/dinhtrung/fleet-console/pull/437)).

### Changed

- A permission refusal is classified instead of returned as an anonymous
  error, raises an operator question, and the preflight proves the forge token
  can write as well as read ([#262](https://github.com/dinhtrung/fleet-console/pull/262), [#267](https://github.com/dinhtrung/fleet-console/pull/267), [#268](https://github.com/dinhtrung/fleet-console/pull/268)).
- Human raises carry their brief as evidence; a rig that cannot send
  `evidence_ref` refuses locally ([#285](https://github.com/dinhtrung/fleet-console/pull/285), [#287](https://github.com/dinhtrung/fleet-console/pull/287)).
- Verdict handling reads the reviewer's text as written: decorated notes and
  abbreviated `ACCEPT`s name the head they abbreviate, and a closing keyword
  quoted as code is not a statement ([#269](https://github.com/dinhtrung/fleet-console/pull/269), [#294](https://github.com/dinhtrung/fleet-console/pull/294), [#351](https://github.com/dinhtrung/fleet-console/pull/351), [#458](https://github.com/dinhtrung/fleet-console/pull/458), [#464](https://github.com/dinhtrung/fleet-console/pull/464)).
- A live dispatch that produces no change request is a stall, not progress
  ([#270](https://github.com/dinhtrung/fleet-console/pull/270)).
- A conflicting change request is repaired, never reviewed ([#358](https://github.com/dinhtrung/fleet-console/pull/358)).
- A merge refusal names `Contents: write`; a GitHub mergeability of unknown
  reads as waiting, not a refusal ([#356](https://github.com/dinhtrung/fleet-console/pull/356), [#357](https://github.com/dinhtrung/fleet-console/pull/357)).
- A rejection refusal says the note is older than the head and what unblocks it
  ([#335](https://github.com/dinhtrung/fleet-console/pull/335)).
- Partial work hands off instead of opening a `Refs` merge request ([#344](https://github.com/dinhtrung/fleet-console/pull/344)).
- A bundle-imported rig resolves its spec, and a failed start keeps the seats
  the rig already has ([#436](https://github.com/dinhtrung/fleet-console/pull/436)).
- A stopped rig reads as stopped and starts on fresh demand, once per backoff
  window, unless a person stopped it ([#354](https://github.com/dinhtrung/fleet-console/pull/354), [#435](https://github.com/dinhtrung/fleet-console/pull/435)).
- A cold rig launch gets five minutes, and a missing daemon is classified and
  carried as a fact ([#350](https://github.com/dinhtrung/fleet-console/pull/350), [#360](https://github.com/dinhtrung/fleet-console/pull/360)).
- The reviewer seat is read from the project config, and the operator question
  carries the exact verdict command ([#387](https://github.com/dinhtrung/fleet-console/pull/387), [#272](https://github.com/dinhtrung/fleet-console/pull/272)).
- Registry edits remove or update one project entry without rewriting the
  config file ([#279](https://github.com/dinhtrung/fleet-console/pull/279)).

### Fixed

- A board answer works for both row shapes a decision lives in ([#256](https://github.com/dinhtrung/fleet-console/pull/256)).
- An accepted change request that cannot merge raises one repair decision
  ([#254](https://github.com/dinhtrung/fleet-console/pull/254)); a blocked merge pages once per item, reason and head ([#255](https://github.com/dinhtrung/fleet-console/pull/255)).
- A malformed verdict is recorded once, not reprinted every tick, and goes to
  the operator after two nudge cycles ([#362](https://github.com/dinhtrung/fleet-console/pull/362), [#366](https://github.com/dinhtrung/fleet-console/pull/366)).
- A stale review that outlives its nudges asks a human once per head
  ([#342](https://github.com/dinhtrung/fleet-console/pull/342)); a question about a superseded head closes itself ([#421](https://github.com/dinhtrung/fleet-console/pull/421)).
- A tick without a clock still raises the stall, and a panicking mirror tick no
  longer ends the loop ([#352](https://github.com/dinhtrung/fleet-console/pull/352), [#353](https://github.com/dinhtrung/fleet-console/pull/353)).
- A live dispatch only blocks a merge on another branch ([#388](https://github.com/dinhtrung/fleet-console/pull/388)).
- The retention guard reads both project subject shapes ([#463](https://github.com/dinhtrung/fleet-console/pull/463)).
- A stale verdict is drawn as its own state with both shas rather than as
  `parked` ([#417](https://github.com/dinhtrung/fleet-console/pull/417)).
- Deleting a project reaches adopted verdicts, so a recreated project cannot
  inherit an `ACCEPT` ([#444](https://github.com/dinhtrung/fleet-console/pull/444)).
- A verdict question row closes when its merge lands ([#381](https://github.com/dinhtrung/fleet-console/pull/381)).
- One unreadable rig no longer blanks the project; capacity sums over the rigs
  that answered ([#447](https://github.com/dinhtrung/fleet-console/pull/447)).
- A skipped verdict appears on the audit surface once per note, and an
  unattributable verdict is recorded and printed once per item, head and note
  ([#296](https://github.com/dinhtrung/fleet-console/pull/296), [#336](https://github.com/dinhtrung/fleet-console/pull/336)).
- A change request that closes no item stays visible and names the item it
  mentioned elsewhere; one that references a closed item raises a decision
  ([#343](https://github.com/dinhtrung/fleet-console/pull/343), [#369](https://github.com/dinhtrung/fleet-console/pull/369)).
- A merge-repair decision closes when its premise no longer holds
  ([#363](https://github.com/dinhtrung/fleet-console/pull/363)).
- Work in flight silences the human question, and an abbreviated head still
  counts ([#355](https://github.com/dinhtrung/fleet-console/pull/355)).
- The reviewer is asked with the exact verdict line, and the verdict the
  reviewer wrote is read before asking the operator
  ([#364](https://github.com/dinhtrung/fleet-console/pull/364), [#373](https://github.com/dinhtrung/fleet-console/pull/373)).
- A verdict on an ancestor of the head is the reviewer's question
  ([#419](https://github.com/dinhtrung/fleet-console/pull/419)).
- A decomposition that asked nobody answers nothing
  ([#449](https://github.com/dinhtrung/fleet-console/pull/449)).

### Internal

- The retention guard and the failed-roster seam gained regression tests
  ([#459](https://github.com/dinhtrung/fleet-console/pull/459), [#462](https://github.com/dinhtrung/fleet-console/pull/462)).
- One helper shadowed by a new type was renamed so `main` compiles ([#423](https://github.com/dinhtrung/fleet-console/pull/423)).

## [v0.2.0] - 2026-11-08

### Added

- **One or more rigs per project, in tiers.** Seats fill tier by tier with a
  spill trigger, and the registry accepts several rigs for one project
  ([#56](https://github.com/dinhtrung/fleet-console/pull/56), [#199](https://github.com/dinhtrung/fleet-console/pull/199), [#207](https://github.com/dinhtrung/fleet-console/pull/207)).
- **Two-phase dispatch and boot adoption.** Open dispatch rows reconcile
  against the rig at boot, and one flock per database keeps a single writer
  ([#52](https://github.com/dinhtrung/fleet-console/pull/52), [#54](https://github.com/dinhtrung/fleet-console/pull/54), [#58](https://github.com/dinhtrung/fleet-console/pull/58)).
- **Project creation from the board**, written back to `config.yaml`, with a
  Projects surface, a New project dialog and a nameable culture file
  ([#74](https://github.com/dinhtrung/fleet-console/pull/74), [#75](https://github.com/dinhtrung/fleet-console/pull/75), [#111](https://github.com/dinhtrung/fleet-console/pull/111)).
- **Preflight and allowlisted setup** for a project that cannot work, with the
  four checks, setup steps and one runtime enum on the board
  ([#79](https://github.com/dinhtrung/fleet-console/pull/79), [#92](https://github.com/dinhtrung/fleet-console/pull/92), [#109](https://github.com/dinhtrung/fleet-console/pull/109)).
- **A Culture screen**: read and write a rig's culture file over HTTP, with
  edit mode and a `409` banner ([#99](https://github.com/dinhtrung/fleet-console/pull/99), [#103](https://github.com/dinhtrung/fleet-console/pull/103)).
- **Epic rollup**: a pure parent rule, a decide-time epic sweep, and closure of
  swept epics on every tick ([#108](https://github.com/dinhtrung/fleet-console/pull/108), [#125](https://github.com/dinhtrung/fleet-console/pull/125), [#128](https://github.com/dinhtrung/fleet-console/pull/128)).
- **Progress notifications**: a notifier that sends once per state, a snapshot
  and renderer for the phone report, post-merge/on-demand reports, and decision
  escalation ([#76](https://github.com/dinhtrung/fleet-console/pull/76), [#78](https://github.com/dinhtrung/fleet-console/pull/78), [#95](https://github.com/dinhtrung/fleet-console/pull/95)).
- **Quiet notifications**: a quiet snapshot sends nothing; a board with only
  human obligations is not quiet; one rule decides what waits on a person
  ([#135](https://github.com/dinhtrung/fleet-console/pull/135), [#145](https://github.com/dinhtrung/fleet-console/pull/145), [#151](https://github.com/dinhtrung/fleet-console/pull/151)).
- **Decisions on the item**: a raised decision is posted as one note, and the
  reply is read back and resolved through the board's call
  ([#194](https://github.com/dinhtrung/fleet-console/pull/194), [#200](https://github.com/dinhtrung/fleet-console/pull/200)).
- **Board review controls**: approve or reject a change request from the board;
  the drawer reads its change request from the forge snapshot and skips a
  closed-unmerged one ([#148](https://github.com/dinhtrung/fleet-console/pull/148), [#150](https://github.com/dinhtrung/fleet-console/pull/150), [#152](https://github.com/dinhtrung/fleet-console/pull/152)).
- **The task drawer reads the dispatch seat's pane** ([#187](https://github.com/dinhtrung/fleet-console/pull/187)).
- **Settings on the board** read and write the real configuration through a
  guarded write path ([#192](https://github.com/dinhtrung/fleet-console/pull/192), [#198](https://github.com/dinhtrung/fleet-console/pull/198)).
- **Audit surfaces**: page and filter the event log, serve one event, and
  render `/audit` and `/audit/:id` from it ([#183](https://github.com/dinhtrung/fleet-console/pull/183), [#186](https://github.com/dinhtrung/fleet-console/pull/186)).
- **Housekeeping**: keep/remove rules, safe worktree removal, and an audited
  interval run ([#184](https://github.com/dinhtrung/fleet-console/pull/184), [#195](https://github.com/dinhtrung/fleet-console/pull/195)).
- **Fleet lifecycle**: stop an idle rig and start one when work arrives
  ([#185](https://github.com/dinhtrung/fleet-console/pull/185)).
- **Mirror idle behaviour**: back off the poll while the fleet is idle, and a
  wake channel that ends the idle cadence ([#193](https://github.com/dinhtrung/fleet-console/pull/193), [#197](https://github.com/dinhtrung/fleet-console/pull/197)).
- **A signed forge webhook** that wakes the loop ([#201](https://github.com/dinhtrung/fleet-console/pull/201)).
- **Verdict rules**: a verdict on a moved head asks for a re-review, the
  verdict note is its first line, and the verdict is read from the change
  request rather than the item ([#53](https://github.com/dinhtrung/fleet-console/pull/53), [#89](https://github.com/dinhtrung/fleet-console/pull/89), [#91](https://github.com/dinhtrung/fleet-console/pull/91)).
- **Change requests with no dispatch row are adopted**, so hand-opened work is
  tracked like dispatched work ([#140](https://github.com/dinhtrung/fleet-console/pull/140)).
- **Registry reconciliation**: projects absent from the file are disabled, and
  `--prune` runs only without a live dispatch ([#55](https://github.com/dinhtrung/fleet-console/pull/55)).

### Changed

- Per-item forge reads run in parallel, and the project timeout scales with
  them ([#57](https://github.com/dinhtrung/fleet-console/pull/57)).

### Fixed

- The culture guard runs before the first read and derives only inside a real
  rig ([#87](https://github.com/dinhtrung/fleet-console/pull/87)).
- One contract covers a failed audit write in the merge path ([#100](https://github.com/dinhtrung/fleet-console/pull/100)).
- An I/O refusal on `POST /api/projects` is a `500`, not a `409` ([#80](https://github.com/dinhtrung/fleet-console/pull/80)).
- A post-merge forge failure is a gap, not a failed merge ([#102](https://github.com/dinhtrung/fleet-console/pull/102)).
- Every `gh` verb carries `-R`, so a write cannot depend on the current
  directory ([#113](https://github.com/dinhtrung/fleet-console/pull/113)).
- Compat is re-probed while the gate is closed, so a boot race cannot freeze
  the act loop ([#131](https://github.com/dinhtrung/fleet-console/pull/131)).
- A closed change request no longer blocks a re-dispatch ([#205](https://github.com/dinhtrung/fleet-console/pull/205)).
- A decision the tick's own capture answered is not raised again ([#146](https://github.com/dinhtrung/fleet-console/pull/146)).
- An epic item is recognised on the first read, and an openspec change set is
  documentation rather than code ([#155](https://github.com/dinhtrung/fleet-console/pull/155), [#181](https://github.com/dinhtrung/fleet-console/pull/181)).

### Removed

- The mock screens are deleted, with a guard against their return ([#208](https://github.com/dinhtrung/fleet-console/pull/208)).

### Internal

- Every CI job is bounded, so one wedged runner cannot block the fleet
  ([#94](https://github.com/dinhtrung/fleet-console/pull/94)).
- Config loads get an explicit `Env` in tests, and the tier dispatch, epic
  sweep and adoption paths gained end-to-end tests
  ([#97](https://github.com/dinhtrung/fleet-console/pull/97), [#116](https://github.com/dinhtrung/fleet-console/pull/116), [#127](https://github.com/dinhtrung/fleet-console/pull/127), [#129](https://github.com/dinhtrung/fleet-console/pull/129), [#153](https://github.com/dinhtrung/fleet-console/pull/153), [#210](https://github.com/dinhtrung/fleet-console/pull/210)).
- The homebox install for the traffic-simulator-v2 and mnpserver2 rigs is
  documented ([#61](https://github.com/dinhtrung/fleet-console/pull/61)).

### Documentation

- Documented the epic rollup, the rig lifecycle and idle cadence, and the reply
  line and author rule ([#121](https://github.com/dinhtrung/fleet-console/pull/121), [#206](https://github.com/dinhtrung/fleet-console/pull/206), [#209](https://github.com/dinhtrung/fleet-console/pull/209)).

## [v0.1.3] - 2026-10-29

### Fixed

- GitLab change-request details are fetched only for candidates, and forge
  failures are isolated ([#43](https://github.com/dinhtrung/fleet-console/pull/43)).

### Documentation

- Recorded the six operating decisions from the first real install.

## [v0.1.2] - 2026-10-29

### Added

- The act loop executes the decide plan through the dispatch, verdict and merge
  watchdog path ([#41](https://github.com/dinhtrung/fleet-console/pull/41)).

### Fixed

- Rig seat reads target the fleet, so compat works outside a session ([#40](https://github.com/dinhtrung/fleet-console/pull/40)).

## [v0.1.1] - 2026-10-29

### Added

- `config.yaml` is strict, supports `--apply-config`, and prints redacted
  effective settings with each value's source ([#36](https://github.com/dinhtrung/fleet-console/pull/36)).

### Documentation

- Added usage, settings and a quickstart ([#37](https://github.com/dinhtrung/fleet-console/pull/37)).

## [v0.1.0] - 2026-10-28

### Added

- **The Go service**: Fiber v3 API, SQLite store and rig compat probe ([#12](https://github.com/dinhtrung/fleet-console/pull/12)).
- **The web foundation**: shadcn/ui with sage as the only appearance ([#13](https://github.com/dinhtrung/fleet-console/pull/13)).
- **Installer and packaging**: a single binary and an upgrade path
  ([#15](https://github.com/dinhtrung/fleet-console/pull/15)).
- **Rig adapters**: typed CLI adapters, a generated spec and the launch
  lifecycle ([#16](https://github.com/dinhtrung/fleet-console/pull/16)).
- **Decide**: the dispatcher rules ported with a golden oracle ([#14](https://github.com/dinhtrung/fleet-console/pull/14)).
- **Forge adapters**: normalized GitHub and GitLab reads and writes ([#17](https://github.com/dinhtrung/fleet-console/pull/17)).
- **The board**: decisions, seats and the task drawer on the fleet API ([#18](https://github.com/dinhtrung/fleet-console/pull/18)).
- **The mirror**: a read-only mirror, decide dry-run and cutover comparison
  ([#25](https://github.com/dinhtrung/fleet-console/pull/25)).
- **The act loop**: dispatch, row actions, verdict capture and the merge
  watchdog ([#30](https://github.com/dinhtrung/fleet-console/pull/30)).
- **P4**: Telegram escalation, the quota guard, state import and a
  multi-project proof ([#33](https://github.com/dinhtrung/fleet-console/pull/33)).

### Changed

- The board reads dispatches and verdicts once per project instead of once per
  row ([#31](https://github.com/dinhtrung/fleet-console/pull/31)).

### Fixed

- The board session is minted only after the operator token is proven ([#26](https://github.com/dinhtrung/fleet-console/pull/26)).
- Every external CLI call and the HTTP server are bounded ([#28](https://github.com/dinhtrung/fleet-console/pull/28)).
- Pending human decisions are listed with the row's real options ([#27](https://github.com/dinhtrung/fleet-console/pull/27)).
- A failed compat probe blocks the act loop and the board ([#29](https://github.com/dinhtrung/fleet-console/pull/29)).
- A `403` is classified as a forbidden operation, not as stale state ([#32](https://github.com/dinhtrung/fleet-console/pull/32)).

### Internal

- CI gates pull requests on Go build/vet/test, web typecheck/lint/build and
  OpenSpec validation.
- The release workflow builds deterministic linux amd64/arm64 tarballs, emits
  sha256 sums and publishes a GitHub Release.

[Unreleased]: https://github.com/dinhtrung/fleet-console/compare/v0.4.0...HEAD
[v0.4.0]: https://github.com/dinhtrung/fleet-console/compare/v0.3.0...v0.4.0
[v0.3.0]: https://github.com/dinhtrung/fleet-console/compare/v0.2.0...v0.3.0
[v0.2.0]: https://github.com/dinhtrung/fleet-console/compare/v0.1.3...v0.2.0
[v0.1.3]: https://github.com/dinhtrung/fleet-console/compare/v0.1.2...v0.1.3
[v0.1.2]: https://github.com/dinhtrung/fleet-console/compare/v0.1.1...v0.1.2
[v0.1.1]: https://github.com/dinhtrung/fleet-console/compare/v0.1.0...v0.1.1
[v0.1.0]: https://github.com/dinhtrung/fleet-console/releases/tag/v0.1.0
