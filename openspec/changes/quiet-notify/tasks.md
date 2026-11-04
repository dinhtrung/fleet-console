## 0. Ground rules

- Build gate for every task: `make check` (or `go build ./... && go vet ./... && go test ./...`) — paste the real output into the PR body.
- No new environment variable, no config key, no forge or store change. This change is behaviour + tests + docs.
- `notify.configured()` stays the first short-circuit: no credentials, no send, no new log noise.
- Behaviour that is not in `specs/progress-notify/spec.md` is not yours to invent. If the quiet predicate turns out
  to need an input the snapshot does not have, **stop and report** instead of adding a live read inside the
  notifier.

## 1. Quiet predicate (builder 1)

- [ ] 1.1 Add `Quiet() bool` on `progress.Snapshot` (or a `Counts.OpenDecisions` field first, if it is missing),
      computed only from `Counts` — never from the truncated slices, never from the rendered text. Fill the new
      count where the snapshot is built so the renderer and the predicate agree by construction.
- [ ] 1.2 Unit tests in `internal/progress/progress_test.go`: quiet when everything is zero; NOT quiet for each of
      the five non-zero cases (one open epic, one in flight, one pending, one on hold, one open decision) — five
      separate cases, not one table row with all five set, so a partial predicate fails loudly. Add one case where
      the list is truncated but the count is non-zero → not quiet.
- [ ] 1.3 Evidence: the test names and the `go test ./internal/progress/...` output in the PR body.

## 2. Gate the notice (builder 1, after 1.1)

- [ ] 2.1 `cmd/fleet-console/notify.go`: in `sendProgress`, return a `skipped` result (or an explicit zero-result)
      **before** rendering and **before** `alert.Notifier.Notify` when `snapshot.Quiet()`, and write exactly one log
      line naming the reason. Do not touch the trigger conditions in `main.go`.
- [ ] 2.2 Tests in `cmd/fleet-console/notify_test.go` against the existing fake Bot API, counting `sendMessage`
      calls: (a) quiet snapshot + digest trigger → 0 calls; (b) quiet snapshot from a merge tick → 0 calls;
      (c) non-quiet snapshot → 1 call; (d) `--notify-report` on a quiet snapshot → 1 call, exit 0; (e) quiet skip
      followed by a non-quiet identical-to-earlier body → 1 call (dedupe not poisoned).
- [ ] 2.3 Evidence: the five test names plus the count assertions, and the real `go test ./cmd/... ./internal/...` output.

## 3. Audit the other senders + docs (builder 2, independent)

- [x] 3.1 Enumerate every path that can produce a Telegram message — `notify.escalate`, `sendProgress` (digest and
      merge triggers), `--notify-report`, `internal/alert/*` callers, and any cron/unit file under `deploy/` that
      invokes `--notify-report`. For each, state in the PR body whether it can emit a notice while the snapshot is
      quiet, with `path:line`. The expected answer is: only `--notify-report`, which is the intended exception.
- [x] 3.2 If the audit finds a deploy/cron entry that reports on a schedule (a heartbeat), name it and propose the
      concrete change in the PR body — **do not** change deploy files in this row unless the finding is a scheduled
      report that the spec's exception does not cover; in that case make the minimal edit and say so explicitly.
- [x] 3.3 Docs: the notice section of `docs/cutover.md` § Telegram and a `## Progress notices` section in `PRODUCT.md`
      state the quiet rule, the log line, and the `--notify-report` exception. One paragraph each, no re-documenting
      of the message format. (`DESIGN.md` was named in the original brief, but it is the inherited design system —
      colors, typography, components — and documents no notice behaviour. `docs/cutover.md` is where the two message
      kinds are described, so the rule went there.)
- [x] 3.4 Evidence: the audit table (path → can it send while quiet → why), the docs diff, `git diff --stat`.

## 4. Orchestrator

- [ ] 4.1 After both PRs merge: verify on the box that an idle digest tick logs the skip line and sends nothing
      (quote the log), then close the epic.
