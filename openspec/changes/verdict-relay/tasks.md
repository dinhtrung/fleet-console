# Tasks — verdict relay

A box is ticked only with the command that proved it, quoted in the pull request.

## 0. Ground rules

Fixed here so two builders cannot invent two names. Anything this section does not fix is a question for the issue,
not a guess.

**Config block** (per project, strict decode — an unknown key is a load error):

```yaml
projects:
  - key: fleet-console
    verdict_relay:            # absent = off, today's behaviour
      mode: scribe            # scribe | operator
      seat: verdict-scribe    # required when mode is set; no suffixing, no default
```

Go names: `config.Project.VerdictRelay *config.VerdictRelay`, `type VerdictRelay struct { Mode string; Seat string }`
with `yaml:"mode"` / `yaml:"seat"`, constants `config.RelayModeScribe = "scribe"`, `config.RelayModeOperator =
"operator"`, and a resolved `Config.RelayFor(projectKey string) (mode, seat string)` helper returning `("", "")` for
an absent block. Validation lives in `config.ValidateProject` and answers a field error on `verdict_relay.mode` or
`verdict_relay.seat`.

**Plan vocabulary:** `decide.IntentRelayVerdict IntentKind = "relay_verdict"`, carrying the existing `Item`,
`ChangeRequest`, `HeadSHA` and `Text` fields; a relay never sets `Seat` (the act side resolves it from the config).

**Events:** plan-side `verdict.relayed` (actor `decide`, subject `#<item>`, detail `head=<sha>; row <qitemId>` after
the row exists, `head=<sha>; planned` before). Capture-side: the existing `verdict.capture` detail gains `; via=<seat>`
only when the note carried a `via:` line.

**Note grammar additions (and nothing else):** line 1 unchanged (`ACCEPT <sha>` / `REJECT <sha>`); line 2 MAY be
`via: <seat>`; capture parses exactly `^via:\s*(\S+)$` on the second non-empty line. A malformed `via:` line is
ignored, never an error.

**Brief:** `<worktreeRoot>/briefs/relay-<key>-<item>-<head8>.md` — the same `briefs/` directory and `os.TempDir()`
fallback every other raise uses. The brief MUST carry: every note body verbatim with its note id, the full 40-char
head sha, the item and change request numbers, the exact posting command, the required `via:` line, and the
INCONCLUSIVE instruction.

**Allowed answers:** a verdict note, or INCONCLUSIVE by answering the row (`denied` / `no-follow-on`). Merging,
closing, editing code and queue writes are explicitly forbidden in the brief text.

**Log lines:** none new. A relay shows up through the existing `act: <key> decisions=/skipped=` accounting and the
`act-skip:` line when a raise fails.

**Build gate:** `go build ./... && go vet ./... && go test ./...`, `npm run typecheck && npm run lint && npm run
build` (only if `src/` changed), `openspec validate --all --strict`.

## 1. Config (one issue: `internal/config`)

- [ ] 1.1 `VerdictRelay` block on `config.Project`, decoded strictly, resolved into the effective config with a
      `RelayFor(key)` accessor. Verify: `go test ./internal/config` — table of absent / scribe / operator /
      unknown-mode / mode-without-seat.
- [ ] 1.2 `ValidateProject` refuses `mode` outside the two constants and `seat` empty when `mode` is set, with a
      `FieldError` on `verdict_relay.mode` / `verdict_relay.seat`. Verify: the existing validation table gains the two
      new rows, and the loader answers the same message as the board's `422`.
- [ ] 1.3 `--print-config` prints the effective mode and seat per project, redacted like everything else (no secret
      here, but it must appear so an operator can prove what the running process read). Verify: a run against a
      fixture file, quoted in the PR.

## 2. Decide — the trigger (one issue: `internal/decide`)

- [ ] 2.1 `IntentRelayVerdict` + the arm described in design D1: open change request, item open, no verdict at the
      head (abbreviated verdicts count — reuse `sameSHA` from the abbreviation epic), a terminal reviewer row naming
      that head, no existing `verdict.relayed` for that head. Verify: `go test ./internal/decide -run
      TestPlanRelay...` with a fake snapshot per branch (pending row, done row, verdict at head, moved head).
- [x] 2.2 Dedupe is the ledger, not memory: the intent's event carries `head=<sha>;` and the arm reads
      `HasEvent("verdict.relayed", "#<item>", "head=<sha>;")`. Verify: two plans over one snapshot differ by zero
      relays once the ledger holds the event.
- [x] 2.3 Relay is planned only when the project names a mode. Verify: the same snapshot with an absent block yields
      no relay intent and unchanged `decision.*` intents.

## 3. Act — the raise, the destination, the marker (one issue: `internal/act`)

- [ ] 3.1 `raiseVerdictRelay` (new `internal/act/relay.go`), modelled on `raiseOperatorQuestion`
      (`review.go:120-160`): read the notes with `Forge.CRNotes(changeRequest)`, write the brief per §0, create one
      row to the configured seat with `Source: "human@kernel"` and a summary naming the item, head and "review has no
      verdict". Verify: handler-level test with `forge.NewFake` — the brief contains every note body verbatim, the
      40-char head, the command and the `via:` line; one create, one destination.
- [ ] 3.2 Operator mode: resolve the destination for operator questions from the project's relay config
      (`tick.go:256` → `RaiseOperatorQuestion`), falling back to `human@kernel`. Verify: two projects in one tick,
      one with `mode: operator` → destination is the seat, one without → `human@kernel`.
- [ ] 3.3 Capture parses the `via:` marker and records it in the `verdict.capture` detail; an absent or malformed
      line records nothing. Verify: `go test ./internal/act -run TestCaptureRecordsRelayMarker`.
- [ ] 3.4 An INCONCLUSIVE row answer raises the operator question on the next tick and writes nothing on the forge.
      Verify: test drives the relay intent, answers the row, and asserts one operator question and zero forge writes.

## 4. Docs (one issue, docs-only PR allowed)

- [ ] 4.1 `docs/fleet-console.md`: a short "When a review is not a verdict" subsection — the two modes, the trigger,
      the `via:` marker, and the sentence that the relay never merges.
- [ ] 4.2 `docs/quickstart.md`: the block to copy, with `mode: scribe` and a seat name, and what happens if it is
      absent.

## 5. Gate

- [ ] 5.1 `go build ./... && go vet ./... && go test ./...` — full output in the PR.
- [ ] 5.2 `openspec validate verdict-relay --type change --strict` and `openspec validate --all --strict`, totals
      quoted.
- [ ] 5.3 `make build`, then a spare-port instance with a throwaway config, database and `act: false`
      (`scripts/spare-port-route-probe.sh`) whose file carries the block: `--print-config` shows it and a bogus mode
      refuses to start. Never on 8787.
