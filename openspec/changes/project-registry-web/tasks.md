# Tasks — project registry written from the board

A box is ticked only with the command that proved it, quoted in the pull request.

## 1. Backend — the writer and the two routes (issue: backend)

- [ ] 1.1 `internal/configfile`: `UpsertProject` over a `yaml.v3` node tree — comments, key order and untouched projects survive; a missing `projects:` key is created; an existing key is `Unchanged`. Verify: `go test ./internal/configfile` with a fixture file that carries a header comment and two projects, asserting the resulting text.
- [ ] 1.2 The same package writes atomically: temp file in the same directory, `fsync`, original mode, `rename`. Verify: test asserts the temp file is gone after both success and a forced encode failure, and the original bytes survive the failure.
- [ ] 1.3 Stale-read guard: the write compares the on-disk sha256 with the one the request read and refuses a mismatch. Verify: test touches the file between read and write, expects the refusal, expects the file untouched.
- [ ] 1.4 Export one `config.ValidateProject` and call it from the loader and the handler. Verify: table test runs the same bad project through both entry points and compares the messages.
- [ ] 1.5 `internal/web/projects.go`: `GET /api/projects` and `POST /api/projects` inside the authenticated group, with `201` / `400` / `409` / `422` as the design says, `warnings` carried in the success body. Verify: `httptest` table in `projects_test.go` covering each status.
- [ ] 1.6 The handler upserts the project into the store after a successful write, on the same code path as `--apply-config`; a failed write leaves the store untouched. Verify: handler test with a temp DB asserts the row exists after `201` and does not after `409`/`422`.
- [ ] 1.7 Round trip: a file the board wrote is accepted by `--print-config` and `--apply-config`, and the second apply reports `unchanged`. Verify: integration test that POSTs, prints, applies twice.
- [ ] 1.8 Rebuild the embedded board (`internal/webui/dist`) in the same commit so the binary and the source agree. Verify: `make build` then `./fleet-console --version` and `GET /` still 200.

## 2. Web — the Projects surface (issue: web)

- [ ] 2.1 `src/lib/fleet.ts`: `listProjects()` and `createProject(input)` typed against the design's shapes, with the `422` field map surfaced as a typed error rather than a string.
- [ ] 2.2 `src/screens/ProjectsScreen.tsx` + route `/projects` + nav entry in the Board group: table of projects (key, name, forge badge, repo, rigs, cap), empty state, error state with retry.
- [ ] 2.3 New project dialog: every field the file accepts, client-side rules mirroring the server's, per-field server errors rendered on the field, `warnings` rendered as a non-blocking note after success.
- [ ] 2.4 Success path: dialog closes, a toast names the project, and both `["projects"]` and `["board"]` are invalidated so the project is visible without a reload.
- [ ] 2.5 The PR carries before/after screenshots of `/projects` (the reviewer requires them for any `src/` diff) and says what was not verified.

## 3. Gate

- [ ] 3.1 `go build ./... && go vet ./... && go test ./...`, then `npm run typecheck && npm run lint && npm run build`, then `openspec validate --all --strict`. Verify: the three commands' real output is quoted in the pull request.
