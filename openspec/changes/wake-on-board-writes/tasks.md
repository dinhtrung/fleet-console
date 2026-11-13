# Tasks — a board write asks the mirror loop for a tick

A box is ticked only with the command that proved it, quoted in the pull request.

## 0. Ground rules

- Gate: `go build ./... && go vet ./... && go test ./...`; `openspec validate wake-on-board-writes --strict`.
  Frontend gates only if `src/` changed (it does not).
- The loop's cadence arithmetic is not the defect: do not lower `idle_poll_max`, do not add a second poll. The wake is
  the existing `mirror.Waker`.
- Read `design.md` D1–D3 before the first line of code. D3 says the verdict route is in scope; D4 says what stays out.

## 1. Project routes — `internal/web/projects.go`, `internal/web/app.go`

- [x] 1.1 Add the `wake(*mirror.Waker)` helper (nil-safe) and pass `cfg.Waker` into the create, edit, pause and delete
      handlers. Verify:
      `go test ./internal/web -run 'TestCreateProjectStoresAndPreserves|TestProjectEditStatuses|TestProjectDelete' -v`.
- [x] 1.2 Call it on the success path only — after the file write, the registry apply and the audit row, before the
      response. A refused request wakes nothing. Verify: the same tests plus
      `go test ./internal/web -run TestCreateProjectStatuses -v`.
- [x] 1.3 Prove the wake: one test that creates, edits and deletes through the real routes with a `mirror.NewWaker()`
      and asserts the channel receives after each. Verify:
      `go test ./internal/web -run TestProjectWritesWakeTheLoop -v`.

## 2. Verdict route — `internal/web/verdict.go`, `internal/web/fleet.go`

- [x] 2.1 `Fleet` carries `Waker`; `web.New` sets it from `Config.Waker`. Verify: `go build ./...`.
- [x] 2.2 Wake after the verdict comment is posted (and after `Ready` when the accept readies a draft), not before.
      Verify: `go test ./internal/web -run TestVerdict -v`.

## 3. Gate

- [x] 3.1 `go build ./... && go vet ./...` clean.
- [x] 3.2 `go test ./internal/web` → 152 passed (the file's own suite; the pre-existing loop-timing failure in
      `internal/mirror` reproduces on `main` and is not this change).
- [x] 3.3 `openspec validate wake-on-board-writes --strict` and `openspec validate --all --strict` green; quote the
      totals.
- [ ] 3.4 After this build is installed on the box: create a project on the board while the journal shows a long
      cadence, and confirm a `mirror:` line follows within seconds instead of after `idle_poll_max`. Not verifiable
      against the running binary until the deploy step picks the build up.
