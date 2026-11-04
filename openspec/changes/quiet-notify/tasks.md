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
      Verified: the audit table is in this row's PR body. Every path in the tree was read: `notifier.escalate`
      (`cmd/fleet-console/notify.go:93`, called from `main.go:541`), `sendProgress` on the merge/digest trigger
      (`notify.go:116`, called from `main.go:564` behind `main.go:559`), `runNotifyReport` (`notify.go:178`,
      flag at `main.go:204`, call at `main.go:329`), `Notifier.Notify`/`send` (`internal/alert/notifier.go:83,126`),
      `Telegram.Send`/`Edit` (`internal/alert/telegram.go:87,94`), and `Escalator.Escalate`
      (`internal/alert/telegram.go:137`). Only `runNotifyReport` can emit while the snapshot is quiet, and the
      spec names it as the intended exception.
- [x] 3.2 If the audit finds a deploy/cron entry that reports on a schedule (a heartbeat), name it and propose the
      concrete change in the PR body — **do not** change deploy files in this row unless the finding is a scheduled
      report that the spec's exception does not cover; in that case make the minimal edit and say so explicitly.
      Verified: no tracked cron/unit/deploy entry invokes `--notify-report` (`git ls-files deploy/` holds only
      `fleet-console.service`, `rig-daemon.service` and `rig.yaml.tmpl`). One untracked box script does:
      `/home/dinhtrung/bin/fc-notify-deploy.sh:79`, driven by `fc-notify-deploy.timer` every 10 minutes. It is not
      a heartbeat: it is gated by the `notify-deployed-sha` marker at line 47 and returns at line 48 unless origin/main
      moved, so the report fires once per deployed commit (log: 19 `deploying`, 19 `notify-report delivered`).
      A scheduled report the spec's exception does not cover would be a defect; this is a deploy receipt, so it is
      named for the owner and left unchanged. No deploy file was edited in this row.
- [x] 3.3 Docs: the notice section states the quiet rule, the log line, and the `--notify-report` exception. One
      paragraph each, no re-documenting of the message format. Verified: `PRODUCT.md` gains a `## Notices` section
      (`PRODUCT.md:22`) and `docs/cutover.md` §Telegram gains one paragraph (`docs/cutover.md:155`). `DESIGN.md` is
      the inherited *visual* design system — it has no §5 and no notification content at all (`grep -c '^## ' → 10
      visual sections; `grep -ci 'notif|telegram|progress' → 0`), and `openspec/project.md:20` forbids putting
      behaviour there ("a rule that exists only in `DESIGN.md` is behaviour nobody approved"). The prior notice
      change put its docs in `docs/cutover.md` §Telegram (`openspec/changes/progress-notify/tasks.md:25`), so this
      row follows that precedent; the brief's "DESIGN.md §5" target does not exist in this repository.
- [x] 3.4 Evidence: the audit table (path → can it send while quiet → why) is in the PR body; the docs diff and
      `git diff --stat` are quoted there too. Verified: `openspec validate --changes quiet-notify --strict` → 11
      passed, 0 failed. The docs diff is two files, `PRODUCT.md` and `docs/cutover.md`; `git diff --stat --
      PRODUCT.md docs/cutover.md` → `PRODUCT.md | 8 ++++++++`, `docs/cutover.md | 9 +++++++++`, 2 files changed,
      17 insertions(+). This file's own diff is excluded from that figure because editing it changes the count.

## 4. Orchestrator

- [ ] 4.1 After both PRs merge: verify on the box that an idle digest tick logs the skip line and sends nothing
      (quote the log), then close the epic.
